package core

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/guohuiyuan/music-lib/model"
)

var previewTitle = regexp.MustCompile(`(?i)\bpreview\b|试听|片段|节选`)

// IsPreviewSong only uses available evidence. Unknown duration is checked after
// download; Apple currently exposes previews, not complete downloadable audio.
func IsPreviewSong(song model.Song) bool {
	if song.Source == "apple" || previewTitle.MatchString(song.Name) {
		return true
	}
	for _, key := range []string{"preview", "is_preview", "trial", "is_trial"} {
		value := strings.ToLower(strings.TrimSpace(song.Extra[key]))
		if value == "1" || value == "true" {
			return true
		}
	}
	return false
}

// WithDownloadMetadata keeps duration across existing browser/bridge DTOs, whose
// extra JSON is round-tripped when a candidate is submitted to the queue.
func WithDownloadMetadata(song model.Song) model.Song {
	if song.Duration <= 0 {
		return song
	}
	extra := make(map[string]string, len(song.Extra)+1)
	for key, value := range song.Extra {
		extra[key] = value
	}
	extra["musicdl_duration"] = strconv.Itoa(song.Duration)
	song.Extra = extra
	return song
}
