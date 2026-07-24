package main

import (
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Media playback for triggers, using Windows' built-in MCI (mciSendString via
// winmm.dll) so we can play the mp3 files GINA triggers reference without any
// third-party audio dependency. Each play opens its own aliased device, plays
// asynchronously, then closes after the clip's length, so overlapping trigger
// sounds don't collide. GINA's PlayMediaFile is fire-and-forget; so is this.

var (
	winmm             = syscall.NewLazyDLL("winmm.dll")
	procMCISendString = winmm.NewProc("mciSendStringW")

	// The MCI string interface is process-global; serialize the (quick) command
	// calls. Playback itself is async, so the lock is never held across a clip.
	mciMu       sync.Mutex
	mciAliasCtr uint64

	// Cap concurrent clips so a rapidly-firing trigger can't spawn unbounded
	// devices; extra plays are dropped rather than queued.
	mediaPlaying int32

	// Master audio state shared by TTS and media: volume 0-100, and a mute flag.
	// The speaker control on the Timers tab drives these; both are persisted.
	audioVolume int32 = 100
	audioMuted  int32 // 0 = audible, 1 = muted
)

const maxConcurrentMedia = 8

// initAudioFromSettings loads the persisted volume/mute at startup.
func initAudioFromSettings() {
	s := GetSettings()
	v := s.AudioVolume
	if v <= 0 && !s.AudioMuted {
		v = 100 // never-configured / zero-value → full
	}
	setAudioVolume(v)
	setAudioMuted(s.AudioMuted)
}

func audioVol() int      { return int(atomic.LoadInt32(&audioVolume)) }
func audioIsMuted() bool { return atomic.LoadInt32(&audioMuted) == 1 }

// setAudioVolume clamps to 0-100 and stores the master volume (no persistence).
func setAudioVolume(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	atomic.StoreInt32(&audioVolume, int32(v))
}

func setAudioMuted(m bool) {
	if m {
		atomic.StoreInt32(&audioMuted, 1)
	} else {
		atomic.StoreInt32(&audioMuted, 0)
	}
}

// mciSend runs one MCI command. ret, when non-nil, receives the textual result
// (e.g. a "status ... length" query). Returns the MCI error code (0 = success).
func mciSend(cmd string, ret []uint16) uintptr {
	cptr, err := syscall.UTF16PtrFromString(cmd)
	if err != nil {
		return 1
	}
	var rp uintptr
	var rl uintptr
	if len(ret) > 0 {
		rp = uintptr(unsafe.Pointer(&ret[0]))
		rl = uintptr(len(ret))
	}
	mciMu.Lock()
	r, _, _ := procMCISendString.Call(uintptr(unsafe.Pointer(cptr)), rp, rl, 0)
	mciMu.Unlock()
	return r
}

// playMedia plays an audio file (absolute path) once at the master volume,
// asynchronously. No-ops while muted.
func playMedia(path string) {
	if audioIsMuted() {
		return
	}
	playMediaAt(path)
}

// playMediaSample plays a file regardless of the mute state (an explicit user
// action, e.g. the sample button in the edit dialog), at the master volume.
func playMediaSample(path string) { playMediaAt(path) }

// playMediaAt is the shared player. Silently no-ops on an empty path, when too
// many clips are already playing, or if MCI can't open the file.
func playMediaAt(path string) {
	if path == "" {
		return
	}
	if atomic.AddInt32(&mediaPlaying, 1) > maxConcurrentMedia {
		atomic.AddInt32(&mediaPlaying, -1)
		return
	}
	go func() {
		defer atomic.AddInt32(&mediaPlaying, -1)

		alias := "fbaud" + strconv.FormatUint(atomic.AddUint64(&mciAliasCtr, 1), 10)
		// Open as an MPEG device (handles mp3 audio); fall back to letting MCI pick
		// the device by the file's registered type if that's refused.
		if mciSend(`open "`+path+`" type mpegvideo alias `+alias, nil) != 0 {
			if mciSend(`open "`+path+`" alias `+alias, nil) != 0 {
				return
			}
		}
		defer mciSend("close "+alias, nil)

		// Apply the master volume (MCI takes 0-1000).
		mciSend("setaudio "+alias+" volume to "+strconv.Itoa(audioVol()*10), nil)

		// Clip length in ms, so we know how long to wait before closing.
		durMs := 0
		buf := make([]uint16, 64)
		if mciSend("status "+alias+" length", buf) == 0 {
			durMs, _ = strconv.Atoi(syscall.UTF16ToString(buf))
		}
		if mciSend("play "+alias, nil) != 0 {
			return
		}
		if durMs <= 0 {
			durMs = 5000 // unknown length — assume a short clip
		}
		time.Sleep(time.Duration(durMs+300) * time.Millisecond)
	}()
}
