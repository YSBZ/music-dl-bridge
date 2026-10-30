package netease

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type qualityTransport func(*http.Request) (*http.Response, error)

func (f qualityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadQualityPrefersHires(t *testing.T) {
	if got := preferredDownloadLevels(nil); !reflect.DeepEqual(got, []string{"hires", "lossless", "exhigh"}) {
		t.Fatalf("quality order=%v", got)
	}
}

func TestDownloadQualityRejectsTrialResponse(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = qualityTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://audio.invalid/trial","type":"mp3","freeTrialInfo":{"start":0,"end":30}}]}`))}, nil
	})
	if url, _, err := New("test").getEAPIDownloadURL("123", "lossless"); err == nil || url != "" {
		t.Fatalf("trial accepted: %q, %v", url, err)
	}
}
