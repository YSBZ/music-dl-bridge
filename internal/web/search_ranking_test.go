package web

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/music-lib/model"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRankMusicSearch(t *testing.T) {
	song := func(id, name, artist, rate string) model.Song {
		return model.Song{ID: id, Source: "netease", Name: name, Artist: artist, Extra: map[string]string{"bitrate": rate}}
	}
	tests := []struct {
		name, q string
		songs   []model.Song
		want    string
	}{
		{"artist title", "七里香 周杰伦", []model.Song{song("wrong", "七里香", "别人", "1411"), song("right", "七里香", "周杰伦", "128")}, "right"},
		{"reverse", "周杰伦 七里香", []model.Song{song("wrong", "七里香", "别人", "1411"), song("right", "七里香", "周杰伦", "128")}, "right"},
		{"joined", "周杰伦七里香", []model.Song{song("wrong", "七里香", "别人", "1411"), song("right", "七里香", "周杰伦", "128")}, "right"},
		{"live demoted", "七里香", []model.Song{song("live", "七里香 (Live)", "周杰伦", "1411"), song("studio", "七里香", "周杰伦", "128")}, "studio"},
		{"cover demoted", "七里香", []model.Song{song("cover", "七里香 (Cover 周杰伦)", "别人", "1411"), song("studio", "七里香", "周杰伦", "128")}, "studio"},
		{"explicit live", "七里香 现场版", []model.Song{song("studio", "七里香", "周杰伦", "320"), song("live", "七里香 (Live)", "周杰伦", "128")}, "live"},
		{"quality tie breaker", "七里香", []model.Song{song("low", "七里香", "周杰伦", "128"), song("high", "七里香", "周杰伦", "320 kbps")}, "high"},
		{"bps normalized", "七里香", []model.Song{song("a", "七里香", "周杰伦", "320000"), song("b", "七里香", "周杰伦", "900")}, "b"},
		{"unknown last", "七里香", []model.Song{song("unknown", "七里香", "周杰伦", ""), song("known", "七里香", "周杰伦", "128")}, "known"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rankMusicSearch(tt.q, tt.songs)
			if got[0].ID != tt.want {
				t.Fatalf("first=%s want=%s", got[0].ID, tt.want)
			}
			if len(got) != len(tt.songs) {
				t.Fatal("variants must remain selectable")
			}
		})
	}
}

func TestRankMusicSearchUsesNativeBitrate(t *testing.T) {
	songs := []model.Song{{ID: "low", Name: "稻香", Artist: "周杰伦", Bitrate: 128}, {ID: "high", Name: "稻香", Artist: "周杰伦", Bitrate: 1756}}
	if got := rankMusicSearch("稻香", songs); got[0].ID != "high" {
		t.Fatalf("native bitrate ignored: %+v", got)
	}
}

func TestRankMusicSearchFormatPreference(t *testing.T) {
	songs := []model.Song{
		{ID: "wav", Name: "稻香", Artist: "周杰伦", Ext: "wav", Bitrate: 4608},
		{ID: "mp3", Name: "稻香", Artist: "周杰伦", Ext: "mp3", Bitrate: 320},
		{ID: "flac", Name: "稻香", Artist: "周杰伦", Extra: map[string]string{"available_format": "flac"}, Bitrate: 900},
	}
	got := rankMusicSearch("稻香", songs)
	if got[0].ID != "flac" || got[1].ID != "wav" || got[2].ID != "mp3" {
		t.Fatalf("format order: %+v", got)
	}
}

func TestBrowserDownloadRejectsPreviewRegardlessOfEmbedAndRange(t *testing.T) {
	initCollectionDBForTest(t)
	router := newCollectionTestRouter()
	RegisterMusicRoutes(router.Group(RoutePrefix), router.Group(RoutePrefix))
	for _, byteRange := range []string{"", "bytes=0-100"} {
		req := httptest.NewRequest(http.MethodGet, RoutePrefix+"/download?id=1&source=apple&name=test", nil)
		req.Header.Set("Range", byteRange)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "试听") {
			t.Fatalf("Range=%q status=%d body=%s", byteRange, w.Code, w.Body.String())
		}
	}
}

func TestRankMusicSearchPerformerEvidenceBeforeQuality(t *testing.T) {
	tests := []struct {
		query string
		songs []model.Song
		want  string
	}{
		{"Believer", []model.Song{
			{ID: "wrong", Name: "Believer", Artist: "Li", Bitrate: 900},
			{ID: "right", Name: "Believer", Artist: "Imagine Dragons", Bitrate: 320, Source: "qq"},
			{ID: "support", Name: "Believer", Artist: "Imagine Dragons", Bitrate: 128, Source: "netease"},
		}, "right"},
		{"稻香 DJ", []model.Song{
			{ID: "studio", Name: "稻香", Artist: "周杰伦", Source: "migu", Bitrate: 1756},
			{ID: "support", Name: "稻香", Artist: "周杰伦", Source: "kugou", Bitrate: 128},
			{ID: "dj", Name: "稻香 (DJ版)", Artist: "一支芹菜", Source: "migu", Bitrate: 320},
		}, "dj"},
		{"执子之手", []model.Song{
			{ID: "other", Name: "执子之手", Artist: "安又琪", Bitrate: 1600},
			{ID: "cover", Name: "执子之手 (Cover 宝石Gem、哩哩)", Artist: "王六一", Source: "netease", Bitrate: 320},
			{ID: "target", Name: "执子之手", Artist: "宝石Gem、哩哩", Source: "kugou", Bitrate: 128},
		}, "target"},
		{"稻香", []model.Song{
			{ID: "other", Name: "稻香", Artist: "Lie", Source: "netease", Bitrate: 1800},
			{ID: "target", Name: "稻香", Artist: "周杰伦", Source: "migu", Bitrate: 1756},
			{ID: "second", Name: "稻香", Artist: "周杰伦", Source: "kugou", Bitrate: 128},
		}, "target"},
		{"稻香 周杰伦 Live", []model.Song{
			{ID: "studio", Name: "稻香", Artist: "周杰伦", Source: "migu", Bitrate: 1756},
			{ID: "live", Name: "稻香 (Live)", Artist: "周杰伦", Source: "kugou", Bitrate: 128},
		}, "live"},
		{"稻香", []model.Song{
			{ID: "dj", Name: "稻香 (DJ版)", Artist: "周杰伦", Bitrate: 2000},
			{ID: "studio", Name: "稻香", Artist: "周杰伦", Bitrate: 320},
		}, "studio"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			if got := rankMusicSearch(tt.query, tt.songs); len(got) == 0 || got[0].ID != tt.want {
				t.Fatalf("got %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestRankMusicSearchExcludesPreviewButKeepsUnknownDuration(t *testing.T) {
	songs := []model.Song{
		{ID: "apple", Name: "稻香", Source: "apple", Duration: 223},
		{ID: "clip", Name: "稻香 (31秒片段)", Duration: 31},
		{ID: "trial", Name: "稻香 (试听版)", Duration: 223},
		{ID: "short", Name: "稻香", Duration: 30},
		{ID: "unknown", Name: "稻香"},
		{ID: "full", Name: "稻香", Duration: 223},
	}
	got := rankMusicSearch("稻香", songs)
	if len(got) != 3 {
		t.Fatalf("preview candidates survived: %+v", got)
	}
}

func TestMusicBridgeDefaultSearchUsesFourSources(t *testing.T) {
	got, err := musicBridgeSources(nil)
	want := []string{"kugou", "migu", "qq", "netease"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
}

func TestMusicBridgeSearchCarriesDurationForDownloadValidation(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	router := gin.New()
	registerMusicBridgeRoutes(router.Group("/music"), nil, func(source, query string) ([]model.Song, error) {
		return []model.Song{{ID: "full", Name: query, Source: source, Duration: 223}}, nil
	})
	w := musicBridgeDo(router, http.MethodGet, "/music/api/music-bridge/search?q=test&sources=migu", nil, "test-token")
	var payload struct {
		Songs []musicBridgeSongResponse `json:"songs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || len(payload.Songs) != 1 {
		t.Fatalf("response %s", w.Body.String())
	}
	var extra map[string]string
	if err := json.Unmarshal([]byte(payload.Songs[0].Extra), &extra); err != nil || extra["musicdl_duration"] != "223" {
		t.Fatalf("missing duration: %s", payload.Songs[0].Extra)
	}
}

func TestMusicBridgeSlowSourceKeepsCompletedResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	done := make(chan struct{})
	defer func() { close(release); <-done }()
	got, err := musicBridgeSearchSongs(ctx, func(source, query string) ([]model.Song, error) {
		if source == "qq" {
			<-release
			defer close(done)
		}
		return []model.Song{{ID: source, Name: query}}, nil
	}, "稻香", []string{"kugou", "qq"})
	if err != nil || len(got) != 1 || got[0].Source != "kugou" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestMusicBridgeBusyRequestDoesNotStartPartialSearch(t *testing.T) {
	for range cap(musicBridgeSearchSlots) - 1 {
		musicBridgeSearchSlots <- struct{}{}
	}
	var calls atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := musicBridgeSearchSongs(ctx, func(source, query string) ([]model.Song, error) { calls.Add(1); return nil, nil }, "test", []string{"qq", "netease"})
	for range cap(musicBridgeSearchSlots) - 1 {
		<-musicBridgeSearchSlots
	}
	deadline := time.Now().Add(time.Second)
	for len(musicBridgeSearchSlots) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err == nil || calls.Load() != 0 {
		t.Fatalf("busy request started %d calls, err=%v", calls.Load(), err)
	}
}

func TestMusicBridgeRanksBeforeLimit(t *testing.T) {
	t.Setenv("MUSIC_BRIDGE_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerMusicBridgeRoutes(router.Group("/music"), nil, func(string, string) ([]model.Song, error) {
		return []model.Song{
			{ID: "cover", Name: "七里香 (Cover 周杰伦)", Artist: "其他人"},
			{ID: "studio", Name: "七里香", Artist: "周杰伦"},
		}, nil
	})
	w := musicBridgeDo(router, http.MethodGet, "/music/api/music-bridge/search?q="+url.QueryEscape("七里香 周杰伦")+"&limit=1", nil, "test-token")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var payload struct {
		Songs []musicBridgeSongResponse `json:"songs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Songs) != 1 || payload.Songs[0].ID != "studio" {
		t.Fatalf("unexpected ranked response: %+v", payload.Songs)
	}
}
