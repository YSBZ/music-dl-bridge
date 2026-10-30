package core

import (
	"bytes"
	"github.com/guohuiyuan/music-lib/model"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDetectAudioExtBySignature(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "wav", data: []byte("RIFF1234WAVEfmt "), want: "wav"},
		{name: "riff video is not wav", data: []byte("RIFF1234AVI "), want: ""},
		{name: "flac", data: []byte{'f', 'L', 'a', 'C', 0x00}, want: "flac"},
		{name: "id3 mp3", data: []byte{'I', 'D', '3', 0x04}, want: "mp3"},
		{name: "unknown", data: []byte("not-audio"), want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectAudioExtBySignature(tc.data); got != tc.want {
				t.Fatalf("DetectAudioExtBySignature() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWAVMetadataDoesNotPrependMP3Tag(t *testing.T) {
	data := []byte("RIFF1234WAVEfmt ")
	got, err := EmbedSongMetadata(data, &model.Song{Name: "test", Artist: "test", Album: "test"}, "", nil, "")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("WAV modified as MP3: %q, %v", got, err)
	}
	if AudioMimeByExt("wav") != "audio/wav" || DetectAudioExtByContentType("audio/x-wav") != "wav" {
		t.Fatal("WAV MIME not preserved")
	}
}

func TestParseContentRangeTotal(t *testing.T) {
	total, ok := parseContentRangeTotal("bytes 0-3/61520341")
	if !ok {
		t.Fatal("parseContentRangeTotal() ok = false")
	}
	if total != 61520341 {
		t.Fatalf("parseContentRangeTotal() = %d, want 61520341", total)
	}
}

func TestFetchBytesWithMimeUsesRangeDownload(t *testing.T) {
	payload := append([]byte{'f', 'L', 'a', 'C'}, bytes.Repeat([]byte("audio"), 4096)...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "song.flac", time.Now(), bytes.NewReader(payload))
	}))
	defer server.Close()

	data, contentType, err := FetchBytesWithMime(server.URL, "netease")
	if err != nil {
		t.Fatalf("FetchBytesWithMime returned error: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("FetchBytesWithMime data mismatch: got %d bytes want %d", len(data), len(payload))
	}
	if contentType == "" {
		t.Fatal("FetchBytesWithMime returned empty content type")
	}
}
