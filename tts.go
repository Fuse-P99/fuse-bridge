package main

import (
	"runtime"
	"strings"
	"sync"
	"time"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"
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
	at        time.Time
}

// ttsQueueMax is the real backlog cap: past this, new utterances are dropped
// rather than queued. It only means anything because ttsWorker waits for each
// utterance to finish (see there) — handing everything to SAPI immediately
// would just move an unbounded queue inside SAPI where we can't cap it.
//
// Small on purpose. Combined with the de-duplication in speak(), five pending
// DIFFERENT callouts is already more than anyone can act on.
const ttsQueueMax = 5

// ttsStaleAfter drops an utterance that waited too long to be spoken. In a
// fight, a callout this old is describing something that has already happened
// and is actively in the way of the current one.
const ttsStaleAfter = 10 * time.Second

var (
	ttsCh   = make(chan ttsUtterance, ttsQueueMax)
	ttsOnce sync.Once
)

// Which voice. SAPI's own default is whatever the legacy Control Panel speech
// page says, which nobody opens; the voice people actually pick lives on
// Settings > Time & language > Speech and is stored under Speech_OneCore,
// which SAPI never consults. Left alone, every install talks in Microsoft
// David Desktop no matter what Settings shows. So the worker reads the
// Settings choice and points the SpVoice at it — the OneCore voices run fine
// through SAPI when handed the token directly — and re-checks it before each
// utterance so a change in Settings takes effect without a restart. No choice,
// an uninstalled voice, or any COM failure leaves SAPI's default in place.
// Deliberately not an app setting: Windows already has the picker.
const (
	ttsSettingsVoiceKey   = `Software\Microsoft\Speech_OneCore\Settings\TextToSpeech`
	ttsSettingsVoiceValue = "Voice"
)

// settingsVoiceID returns the token id of the voice chosen on the Windows
// Settings speech page, or "" when none is set.
func settingsVoiceID() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, ttsSettingsVoiceKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	id, _, err := k.GetStringValue(ttsSettingsVoiceValue)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

// setVoiceToken points voice at the SAPI object token with the given id and
// returns that voice's description. "" means the token doesn't exist or SAPI
// refused it, and the voice keeps whatever it had.
func setVoiceToken(voice *ole.IDispatch, id string) string {
	unk, err := oleutil.CreateObject("SAPI.SpObjectToken")
	if err != nil {
		return ""
	}
	defer unk.Release()
	tok, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return ""
	}
	defer tok.Release()
	// A full token path needs no category; CreateIfNotExist=false makes an
	// uninstalled voice an error instead of an empty token.
	if _, err := oleutil.CallMethod(tok, "SetId", id, "", false); err != nil {
		return ""
	}
	// Voice is a by-reference property (propputref): a plain property put
	// comes back DISP_E_MEMBERNOTFOUND.
	if _, err := oleutil.PutPropertyRef(voice, "Voice", tok); err != nil {
		return ""
	}
	desc, err := oleutil.CallMethod(tok, "GetDescription", 0)
	if err != nil {
		return id
	}
	defer desc.Clear()
	return desc.ToString()
}

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
	// Burst suppression: fifty raiders taking the same AE produce fifty
	// identical utterances, and saying it once is the whole value. See
	// audioAllow in audio.go.
	if !audioAllow("tts:" + text) {
		return
	}
	select {
	case ttsCh <- ttsUtterance{text: text, interrupt: interrupt, at: time.Now()}:
	default: // backlog full (ttsQueueMax) — drop rather than stall the log loop
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

	// Follow the Windows Settings voice (see ttsSettingsVoiceKey). curVoice is
	// the last id acted on, success or not, so an unusable choice is tried once
	// per change rather than on every utterance.
	curVoice := ""
	syncVoice := func() {
		want := settingsVoiceID()
		if want == "" || want == curVoice {
			return
		}
		curVoice = want
		if desc := setVoiceToken(voice, want); desc != "" {
			addStatus("Text-to-speech voice: %s", desc)
		} else {
			addStatus("Text-to-speech: the Windows Settings voice is unavailable, using the SAPI default")
		}
	}
	syncVoice()

	for u := range ttsCh {
		// Whatever waited this long is describing a moment that has passed, and
		// speaking it now only delays what's current.
		if time.Since(u.at) > ttsStaleAfter {
			continue
		}
		syncVoice()
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
		// Then wait it out before taking the next one. Without this the worker
		// drains the channel as fast as it can fill, moving the whole backlog
		// inside SAPI — where it is unbounded, un-droppable, and the reason a
		// burst kept talking long after the fight. Waiting here is what makes
		// ttsQueueMax and the staleness check above mean anything.
		//
		// The cost is that an interrupt request waits for the utterance already
		// playing — bounded to one, and with the de-dup above the queue behind
		// it is short.
		_, _ = oleutil.CallMethod(voice, "WaitUntilDone", 15000)
	}
}
