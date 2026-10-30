package core

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guohuiyuan/music-lib/model"
)

func TestDownloadedAudioQualityPolicy(t *testing.T) {
	for _, tt := range []struct {
		name  string
		song  model.Song
		audio audioInspection
		bad   bool
	}{
		{"full mp3", model.Song{Duration: 223}, audioInspection{Codec: "mp3", Duration: 223, Bitrate: 320000}, false},
		{"128k downgrade", model.Song{Bitrate: 1756}, audioInspection{Codec: "mp3", Duration: 223, Bitrate: 128000}, true},
		{"lossless", model.Song{Duration: 223}, audioInspection{Codec: "flac", Duration: 223}, false},
		{"truncated high quality", model.Song{Duration: 223}, audioInspection{Codec: "flac", Duration: 30}, true},
		{"unknown short preview", model.Song{}, audioInspection{Codec: "mp3", Duration: 31, Bitrate: 320000}, true},
		{"complete short original", model.Song{Duration: 31}, audioInspection{Codec: "mp3", Duration: 31, Bitrate: 320000}, false},
		{"aac", model.Song{}, audioInspection{Codec: "aac", Duration: 223, Bitrate: 256000}, false},
		{"unknown bitrate", model.Song{}, audioInspection{Codec: "mp3", Duration: 223}, true},
		{"explicit preview", model.Song{Source: "apple"}, audioInspection{Codec: "aac", Duration: 223, Bitrate: 256000}, true},
		{"invalid duration", model.Song{}, audioInspection{Codec: "flac"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateAudioInspection(tt.song, tt.audio); (err != nil) != tt.bad {
				t.Fatalf("err=%v want failure=%v", err, tt.bad)
			}
		})
	}
}

func TestDownloadedAudioInspectionUsesRealBytes(t *testing.T) {
	ffmpeg, err := ResolveFFmpegPath()
	if err != nil {
		t.Skip("ffmpeg unavailable; run this test in the validation container")
	}
	if _, err := ResolveFFprobePath(); err != nil {
		t.Fatal(err)
	}
	for _, rate := range []string{"128k", "320k"} {
		t.Run(rate, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audio.flac") // Deliberately misleading extension.
			cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=61", "-c:a", "libmp3lame", "-b:a", rate, "-f", "mp3", path)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generate: %s %v", output, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = checkDownloadedAudio(model.Song{Duration: 61}, data)
			if (err != nil) != (rate == "128k") {
				t.Fatalf("rate=%s err=%v", rate, err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/flac")
				_, _ = w.Write(data)
			}))
			defer server.Close()
			out := t.TempDir()
			result, err := SaveSongToFile(&model.Song{ID: "test", Source: "kuwo", URL: server.URL, Name: "test", Artist: "test", Duration: 61}, out, false, false)
			if rate == "128k" {
				files, _ := os.ReadDir(out)
				if err == nil || len(files) != 0 {
					t.Fatalf("low quality committed: result=%+v err=%v files=%v", result, err, files)
				}
			} else if err != nil || result.Ext != "mp3" || result.SavedPath == "" {
				t.Fatalf("valid full audio not saved: %+v %v", result, err)
			}
		})
	}
}

func TestDownloadedAudioInspectionFailsWhenToolMissing(t *testing.T) {
	t.Setenv(ffprobeEnvName, filepath.Join(t.TempDir(), "missing"))
	if _, err := checkDownloadedAudio(model.Song{}, []byte("audio")); err == nil || !strings.Contains(err.Error(), "ffprobe") {
		t.Fatalf("missing tool accepted: %v", err)
	}
}

func TestDownloadedAudioWAVPreservesShortCompleteRecording(t *testing.T) {
	ffmpeg, err := ResolveFFmpegPath()
	if err != nil {
		t.Skip("ffmpeg unavailable; run in validation container")
	}
	path := filepath.Join(t.TempDir(), "original.wav")
	if output, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=31", "-c:a", "pcm_s16le", path).CombinedOutput(); err != nil {
		t.Fatalf("generate WAV: %s %v", output, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(data)
	}))
	defer server.Close()
	result, err := SaveSongToFile(&model.Song{ID: "wav", Source: "kuwo", URL: server.URL, Name: "Short original", Artist: "Test", Album: "Test", Duration: 31}, t.TempDir(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ext != "wav" || result.ContentType != "audio/wav" || !bytes.Equal(result.Data, data) || !strings.HasSuffix(result.SavedPath, ".wav") {
		t.Fatalf("WAV not preserved: ext=%s MIME=%s path=%s", result.Ext, result.ContentType, result.SavedPath)
	}
}
