package web

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/music-lib/model"
	"net/http"
	"net/url"
	"testing"
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
