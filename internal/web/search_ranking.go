package web

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/guohuiyuan/music-lib/model"
)

// Version hints are evidence, not proof of original-performer identity.
var musicVersionGroups = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\blive\b|现场(?:版)?|演唱会`),
	regexp.MustCompile(`(?i)\bcover\b|翻唱`),
	regexp.MustCompile(`(?i)\binstrumental\b|伴奏|纯音乐`),
	regexp.MustCompile(`(?i)\bremix\b|混音|改编`),
	regexp.MustCompile(`(?i)\bdemo\b|试听版`),
}
var musicBrackets = regexp.MustCompile(`\([^)]*\)|（[^）]*）|\[[^\]]*\]|【[^】]*】`)
var musicRateNumber = regexp.MustCompile(`\d+(?:\.\d+)?`)

func musicSearchText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
func musicBaseTitle(s string) string {
	s = musicBrackets.ReplaceAllStringFunc(s, func(part string) string {
		for _, re := range musicVersionGroups {
			if re.MatchString(part) {
				return ""
			}
		}
		return part
	})
	for _, re := range musicVersionGroups {
		s = re.ReplaceAllString(s, "")
	}
	return musicSearchText(s)
}
func musicSearchRate(song model.Song) float64 {
	raw := strings.ToLower(song.Extra["bitrate"])
	n, _ := strconv.ParseFloat(musicRateNumber.FindString(raw), 64)
	if strings.Contains(raw, "mbps") {
		n *= 1000
	} else if n >= 10000 {
		n /= 1000
	}
	return n
}

// rankMusicSearch preserves candidates and their IDs. Missing version metadata
// never establishes originality; relevance precedes version hints and bitrate.
func rankMusicSearch(query string, songs []model.Song) []model.Song {
	type ranked struct {
		song           model.Song
		match, version int
		rate           float64
	}
	q := musicBaseTitle(query)
	items := make([]ranked, 0, len(songs))
	for _, song := range songs {
		title, artist := musicBaseTitle(song.Name), musicSearchText(song.Artist)
		score := 0
		if title != "" && q == title {
			score = 4
		} else if title != "" && artist != "" && (q == artist+title || q == title+artist) {
			score = 6
		} else {
			if title != "" && strings.Contains(q, title) {
				score += 2
			}
			if artist != "" && strings.Contains(q, artist) {
				score += 2
			}
			if score == 0 && q != "" && strings.Contains(title, q) {
				score = 1
			}
		}
		version := 0
		// Look at title/album only: an artist named Alive is not a live version.
		for _, re := range musicVersionGroups {
			requested, found := re.MatchString(query), re.MatchString(song.Name+" "+song.Album)
			if requested && !found {
				version += 2
			} else if !requested && found {
				version++
			}
		}
		items = append(items, ranked{song, score, version, musicSearchRate(song)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.match != b.match {
			return a.match > b.match
		}
		if a.version != b.version {
			return a.version < b.version
		}
		return a.rate > b.rate
	})
	result := make([]model.Song, len(items))
	for i, item := range items {
		result[i] = item.song
	}
	return result
}
