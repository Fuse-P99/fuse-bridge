package main

import (
	"runtime"
	"strings"
	"sync"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// Text-to-speech for triggers, backed by Windows SAPI (SAPI.SpVoice via COM).
// GINA triggers carry three TTS fields — UseTextToVoice, InterruptSpeech and
// TextToVoiceText — on both the trigger itself and its TimerEndingTrigger; the
// engine calls speak() for the ones that are enabled.
//
// A single dedicated goroutine owns the COM apartment and the voice object (COM
// objects are apartment-bound, so every Speak has to run on the same OS thread).
// speak() just hands work to that goroutine over a buffered channel and never
// blocks the log-tail loop.

// SAPI SpeechVoiceSpeakFlags: async so Speak returns immediately (non-interrupt
// utterances then queue inside SAPI and play in order); purge-before-speak
// clears the queue and cuts off whatever is playing (InterruptSpeech).
const (
	sapiSpeakAsync       = 1 // SVSFlagsAsync
	sapiSpeakPurgeBefore = 2 // SVSFPurgeBeforeSpeak
)

type ttsUtterance struct {
	text      string
	interrupt bool
}

var (
	ttsCh   = make(chan ttsUtterance, 16)
	ttsOnce sync.Once
)

// startTTS spins up the speaker goroutine once. Safe to call more than once.
func startTTS() { ttsOnce.Do(func() { go ttsWorker() }) }

// speak queues an utterance. Non-blocking: if the speaker is backed up (or never
// started, e.g. SAPI unavailable) the utterance is dropped rather than stalling
// the caller. interrupt=true cuts off any speech already playing first.
func speak(text string, interrupt bool) {
	if audioIsMuted() {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	select {
	case ttsCh <- ttsUtterance{text: text, interrupt: interrupt}:
	default: // speaker busy/unavailable — drop rather than block the log loop
	}
}

// ttsWorker owns the COM apartment and the SpVoice for the life of the process,
// speaking each queued utterance. If SAPI can't be created it returns and speak()
// calls harmlessly drain into the (never-read) channel and get dropped.
func ttsWorker() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// STA apartment for the SpVoice. An already-initialized thread returns an
	// error we can ignore; a hard failure means CreateObject will fail below.
	_ = ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	defer ole.CoUninitialize()

	unknown, err := oleutil.CreateObject("SAPI.SpVoice")
	if err != nil {
		addStatus("Text-to-speech unavailable: %v", err)
		return
	}
	voice, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		unknown.Release()
		addStatus("Text-to-speech unavailable: %v", err)
		return
	}
	defer voice.Release()
	defer unknown.Release()

	for u := range ttsCh {
		flags := sapiSpeakAsync
		if u.interrupt {
			flags |= sapiSpeakPurgeBefore
		}
		// Apply the master volume (SAPI takes 0-100) before each utterance so a
		// slider change takes effect immediately.
		_, _ = oleutil.PutProperty(voice, "Volume", audioVol())
		// Async: returns straight away. Non-interrupt utterances queue inside SAPI
		// and play sequentially; an interrupt purges the queue and current speech.
		_, _ = oleutil.CallMethod(voice, "Speak", u.text, flags)
	}
}
