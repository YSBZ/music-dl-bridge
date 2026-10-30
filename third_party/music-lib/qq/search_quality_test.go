package qq

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type searchQualityTransport func(*http.Request) (*http.Response, error)

func (f searchQualityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchExposesCatalogueFormatWithoutGuessingFromBitrate(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = searchQualityTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"song":{"list":[{"songmid":"flac","songname":"test","interval":200,"sizeflac":20000000},{"songmid":"mp3","songname":"test","interval":200,"size320":8000000}]}}}`))}, nil
	})
	songs, err := New("").Search("test")
	if err != nil || len(songs) != 2 {
		t.Fatalf("songs=%v err=%v", songs, err)
	}
	if songs[0].Extra["available_format"] != "flac" || songs[1].Extra["available_format"] != "mp3" {
		t.Fatalf("missing catalogue format: %+v", songs)
	}
}
