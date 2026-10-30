package kuwo

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/guohuiyuan/music-lib/model"
)

type qualityTransport func(*http.Request) (*http.Response, error)

func (f qualityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadQualityHighestFirstAndFallback(t *testing.T) {
	for _, available := range []string{"2000kflac", "320kmp3"} {
		t.Run(available, func(t *testing.T) {
			var requested []string
			old := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = old })
			http.DefaultTransport = qualityTransport(func(r *http.Request) (*http.Response, error) {
				quality := r.URL.Query().Get("br")
				requested = append(requested, quality)
				body := `{"data":{"url":""}}`
				if quality == available {
					body = `{"data":{"url":"https://audio.invalid/full"}}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			got, err := New("").GetDownloadURL(&model.Song{ID: "123", Source: "kuwo"})
			want := []string{"2000kflac"}
			if available == "320kmp3" {
				want = []string{"2000kflac", "flac", "320kmp3"}
			}
			if err != nil || got == "" || !reflect.DeepEqual(requested, want) {
				t.Fatalf("requested=%v want=%v url=%q err=%v", requested, want, got, err)
			}
		})
	}
}
