package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/music-lib/model"
)

func TestMusicBridgeRequiresConfiguredBearer(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMusicBridgeRoutes(r.Group("/music"), nil, func(string, string) ([]model.Song, error) {
		return nil, nil
	})

	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/music/api/music-bridge/search?q=test", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if got := request("").Code; got != http.StatusNotFound {
		t.Fatalf("unconfigured bridge status = %d, want %d", got, http.StatusNotFound)
	}
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	if got := request("wrong-token").Code; got != http.StatusUnauthorized {
		t.Fatalf("wrong bearer status = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := request("test-token").Code; got != http.StatusOK {
		t.Fatalf("valid bearer status = %d, want %d", got, http.StatusOK)
	}
}

func TestMusicBridgeSearchReturnsBoundedSongDTO(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var searchedSource, searchedKeyword string
	registerMusicBridgeRoutes(r.Group("/music"), nil, func(source, keyword string) ([]model.Song, error) {
		searchedSource, searchedKeyword = source, keyword
		return []model.Song{
			{ID: "song-1", Source: "", Name: "First", Artist: "Artist", Album: "Album", Cover: "https://cover/1", Extra: map[string]string{"album_id": "a1"}},
			{ID: "song-2", Source: "kuwo", Name: "Second", Artist: "Artist"},
		}, nil
	})

	w := musicBridgeDo(r, http.MethodGet, "/music/api/music-bridge/search?q=needle&sources=kuwo&limit=1", nil, "test-token")
	if w.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", w.Code, w.Body.String())
	}
	if searchedSource != "kuwo" || searchedKeyword != "needle" {
		t.Fatalf("provider called with %q/%q, want kuwo/needle", searchedSource, searchedKeyword)
	}
	var response struct {
		Songs []map[string]json.RawMessage `json:"songs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if len(response.Songs) != 1 {
		t.Fatalf("returned %d songs, want limit 1", len(response.Songs))
	}
	got := response.Songs[0]
	for field, want := range map[string]string{
		"id": "song-1", "source": "kuwo", "name": "First", "artist": "Artist", "album": "Album", "cover": "https://cover/1",
	} {
		var value string
		if err := json.Unmarshal(got[field], &value); err != nil || value != want {
			t.Errorf("song field %s = %s, want %q (err %v)", field, got[field], want, err)
		}
	}
	var extra string
	if err := json.Unmarshal(got["extra"], &extra); err != nil {
		t.Fatalf("extra must be a JSON string: %s (%v)", got["extra"], err)
	}
	var extraFields map[string]string
	if err := json.Unmarshal([]byte(extra), &extraFields); err != nil || extraFields["album_id"] != "a1" {
		t.Fatalf("extra = %q, fields = %#v, err = %v", extra, extraFields, err)
	}
	if len(got) != 7 {
		t.Fatalf("song DTO has unexpected fields: %#v", got)
	}
	w = musicBridgeDo(r, http.MethodGet, "/music/api/music-bridge/search?q=default", nil, "test-token")
	if w.Code != http.StatusOK || searchedSource != "netease" || searchedKeyword != "default" {
		t.Fatalf("default search status/source/keyword = %d/%q/%q, body %s", w.Code, searchedSource, searchedKeyword, w.Body.String())
	}
}

func TestMusicBridgeDownloadIsIdempotentAndReportsQueueStatus(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	if err := core.CloseConfigDB(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "settings.db"))
	t.Cleanup(func() {
		if err := core.CloseConfigDB(); err != nil {
			t.Error(err)
		}
	})
	queue, err := core.OpenDownloadQueue()
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMusicBridgeRoutes(r.Group("/music"), queue, func(string, string) ([]model.Song, error) {
		return nil, nil
	})
	payload := []byte(`{"request_id":"request-1","song":{"id":"song-1","source":"kuwo","name":"Title","artist":"Artist","album":"Album","cover":"https://cover/song","extra":"{\"album_id\":\"a1\"}"}}`)
	for attempt := 0; attempt < 2; attempt++ {
		w := musicBridgeDo(r, http.MethodPost, "/music/api/music-bridge/downloads", payload, "test-token")
		if w.Code != http.StatusAccepted {
			t.Fatalf("POST status = %d, body = %s", w.Code, w.Body.String())
		}
		var response struct {
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.RequestID != "request-1" || response.Status != core.TaskWaiting {
			t.Fatalf("POST response = %#v", response)
		}
	}
	conflictPayload := []byte(`{"request_id":"request-1","song":{"id":"different-song","source":"kuwo","name":"Other"}}`)
	if w := musicBridgeDo(r, http.MethodPost, "/music/api/music-bridge/downloads", conflictPayload, "test-token"); w.Code != http.StatusConflict {
		t.Fatalf("same request_id with another song status = %d, body = %s", w.Code, w.Body.String())
	}
	activeDuplicatePayload := []byte(`{"request_id":"request-2","song":{"id":"song-1","source":"kuwo","name":"Title","artist":"Artist"}}`)
	if w := musicBridgeDo(r, http.MethodPost, "/music/api/music-bridge/downloads", activeDuplicatePayload, "test-token"); w.Code != http.StatusConflict {
		t.Fatalf("another request_id for an active duplicate status = %d, body = %s", w.Code, w.Body.String())
	}
	_, total, _, err := queue.List(1, 50, "")
	if err != nil || total != 1 {
		t.Fatalf("queued task count = %d, err = %v; want one idempotent task", total, err)
	}
	w := musicBridgeDo(r, http.MethodGet, "/music/api/music-bridge/downloads/request-1", nil, "test-token")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", w.Code, w.Body.String())
	}
	var status struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
		Path      string `json:"path"`
		Error     string `json:"error"`
		Warning   string `json:"warning"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.RequestID != "request-1" || status.Status != core.TaskWaiting || status.Path != "" || status.Error != "" || status.Warning != "" {
		t.Fatalf("GET response = %#v", status)
	}

	var queued []core.DownloadTask
	queued, _, _, err = queue.List(1, 50, "")
	if err != nil || len(queued) != 1 {
		t.Fatalf("queue list = %d tasks, err = %v", len(queued), err)
	}
	var input core.DownloadTaskInput
	if err := json.Unmarshal([]byte(queued[0].Payload), &input); err != nil {
		t.Fatal(err)
	}
	if input.Song.Extra["album_id"] != "a1" {
		t.Fatalf("queued extra = %#v", input.Song.Extra)
	}
}

func TestMusicBridgeRejectsMalformedDownload(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	if err := core.CloseConfigDB(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "settings.db"))
	t.Cleanup(func() {
		if err := core.CloseConfigDB(); err != nil {
			t.Error(err)
		}
	})
	queue, err := core.OpenDownloadQueue()
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMusicBridgeRoutes(r.Group("/music"), queue, func(string, string) ([]model.Song, error) {
		return nil, nil
	})
	for _, payload := range []string{
		`{"request_id":"request-1","song":{"id":"song-1","source":"kuwo","extra":"not-json"}}`,
		`{"request_id":"request-1","song":{"id":"song-1","source":"unknown"}}`,
		`{"request_id":"request-1","song":{"source":"kuwo"}}`,
	} {
		w := musicBridgeDo(r, http.MethodPost, "/music/api/music-bridge/downloads", []byte(payload), "test-token")
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid payload status = %d, body = %s", w.Code, w.Body.String())
		}
	}
	_, total, _, err := queue.List(1, 50, "")
	if err != nil || total != 0 {
		t.Fatalf("invalid requests enqueued %d tasks (err %v)", total, err)
	}
}

func musicBridgeDo(r http.Handler, method, path string, body []byte, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://music.test"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMusicBridgeSearchRejectsBadQuery(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMusicBridgeRoutes(r.Group("/music"), nil, func(string, string) ([]model.Song, error) {
		return nil, nil
	})
	for _, target := range []string{
		"/music/api/music-bridge/search",
		"/music/api/music-bridge/search?q=needle&sources=not-a-source",
		"/music/api/music-bridge/search?q=needle&limit=0",
	} {
		w := musicBridgeDo(r, http.MethodGet, target, nil, "test-token")
		if w.Code != http.StatusBadRequest {
			t.Errorf("query %q status = %d, body = %s", target, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

func TestMusicBridgeSearchTimeoutBoundsProviderCalls(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	oldTimeout := musicBridgeSearchTimeout
	musicBridgeSearchTimeout = 50 * time.Millisecond
	t.Cleanup(func() { musicBridgeSearchTimeout = oldTimeout })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var calls atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	defer closeRelease()
	entered := make(chan struct{}, cap(musicBridgeSearchSlots))
	registerMusicBridgeRoutes(r.Group("/music"), nil, func(string, string) ([]model.Song, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return nil, nil
	})
	done := make(chan *httptest.ResponseRecorder, cap(musicBridgeSearchSlots))
	for range cap(musicBridgeSearchSlots) {
		go func() {
			done <- musicBridgeDo(r, http.MethodGet, "/music/api/music-bridge/search?q=slow", nil, "test-token")
		}()
	}
	for range cap(musicBridgeSearchSlots) {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("search providers did not start")
		}
	}
	if got := calls.Load(); got != int32(cap(musicBridgeSearchSlots)) {
		t.Fatalf("active provider calls = %d, want bounded at %d", got, cap(musicBridgeSearchSlots))
	}
	busy := musicBridgeDo(r, http.MethodGet, "/music/api/music-bridge/search?q=busy", nil, "test-token")
	if busy.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated search status = %d, want %d", busy.Code, http.StatusServiceUnavailable)
	}
	for range cap(musicBridgeSearchSlots) {
		select {
		case response := <-done:
			if response.Code != http.StatusGatewayTimeout {
				t.Errorf("timed out provider request status = %d, want %d", response.Code, http.StatusGatewayTimeout)
			}
		case <-time.After(time.Second):
			t.Fatal("search handler did not return on deadline")
		}
	}
	closeRelease()
	deadline := time.Now().Add(time.Second)
	for len(musicBridgeSearchSlots) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(musicBridgeSearchSlots); got != 0 {
		t.Fatalf("provider slots still occupied after releasing test calls: %d", got)
	}
}

func TestMusicBridgeQueueCompletionFields(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	if err := core.CloseConfigDB(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "settings.db"))
	t.Cleanup(func() {
		if err := core.CloseConfigDB(); err != nil {
			t.Error(err)
		}
	})
	queue, err := core.OpenDownloadQueue()
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMusicBridgeRoutes(r.Group("/music"), queue, func(string, string) ([]model.Song, error) {
		return nil, nil
	})
	payloads := map[string][]byte{
		"request-done":   []byte(`{"request_id":"request-done","song":{"id":"song-2","source":"kuwo"}}`),
		"request-failed": []byte(`{"request_id":"request-failed","song":{"id":"song-3","source":"kuwo"}}`),
	}
	for requestID, payload := range payloads {
		if w := musicBridgeDo(r, http.MethodPost, "/music/api/music-bridge/downloads", payload, "test-token"); w.Code != http.StatusAccepted {
			t.Fatalf("POST %s status = %d, body = %s", requestID, w.Code, w.Body.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The queue invokes this callback after claiming each job. Use real queue
	// completion states while avoiding any upstream provider or filesystem work.
	if err := queue.Start(ctx, func(input core.DownloadTaskInput) (core.DownloadTaskResult, error) {
		if input.Song.ID == "song-3" {
			return core.DownloadTaskResult{}, errors.New("upstream unavailable")
		}
		return core.DownloadTaskResult{Status: core.DownloadStatusSuccess, Path: "/downloads/song.mp3", Warning: "cover skipped"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var done int
		for requestID := range payloads {
			w := musicBridgeDo(r, http.MethodGet, "/music/api/music-bridge/downloads/"+requestID, nil, "test-token")
			if w.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, body = %s", requestID, w.Code, w.Body.String())
			}
			var status map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if requestID == "request-done" && status["status"] == core.DownloadStatusSuccess {
				if status["request_id"] != requestID || status["path"] != "/downloads/song.mp3" || status["warning"] != "cover skipped" || status["error"] != "" {
					t.Fatalf("completion response = %#v", status)
				}
				done++
			}
			if requestID == "request-failed" && status["status"] == core.DownloadStatusFailed {
				if status["request_id"] != requestID || status["error"] != "upstream unavailable" || status["path"] != "" || status["warning"] != "" {
					t.Fatalf("failed response = %#v", status)
				}
				done++
			}
		}
		if done == len(payloads) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for requestID, payload := range payloads {
		w := musicBridgeDo(r, http.MethodPost, "/music/api/music-bridge/downloads", payload, "test-token")
		if w.Code != http.StatusAccepted {
			t.Fatalf("repeat POST %s status = %d, body = %s", requestID, w.Code, w.Body.String())
		}
		var response map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		wantStatus := core.DownloadStatusSuccess
		if requestID == "request-failed" {
			wantStatus = core.DownloadStatusFailed
		}
		if response["request_id"] != requestID || response["status"] != wantStatus {
			t.Fatalf("repeat POST %s response = %#v, want status %s", requestID, response, wantStatus)
		}
	}
	_, total, _, err := queue.List(1, 50, "")
	if err != nil || total != int64(len(payloads)) {
		t.Fatalf("terminal task count = %d, err = %v; want %d", total, err, len(payloads))
	}
}
