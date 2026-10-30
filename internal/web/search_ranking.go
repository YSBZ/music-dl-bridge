package web

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/music-lib/model"
)

// Version hints are evidence, not proof of original-performer identity.
var musicVersionGroups = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\blive\b|现场(?:版)?|演唱会`),
	regexp.MustCompile(`(?i)\bcover\b|翻唱`),
	regexp.MustCompile(`(?i)\binstrumental\b|伴奏|纯音乐`),
	regexp.MustCompile(`(?i)\bremix\b|混音|改编`),
	regexp.MustCompile(`(?i)\bdemo\b|试听版`),
	regexp.MustCompile(`(?i)\bdj\b|DJ版|加速版|降速版|变速版|女声版|男声版|童声版|深情版|治愈版|合唱版|钢琴版|3D环绕版`),
}
var musicBrackets = regexp.MustCompile(`\([^)]*\)|（[^）]*）|\[[^\]]*\]|【[^】]*】`)
var musicRateNumber = regexp.MustCompile(`\d+(?:\.\d+)?`)
var musicCoverCredit = regexp.MustCompile(`(?i)(?:\bcover\s*[:：]?\s*|翻唱自\s*)([^)）\]】]+)`)

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
	if song.Bitrate > 0 {
		return float64(song.Bitrate)
	}
	raw := strings.ToLower(song.Extra["bitrate"])
	n, _ := strconv.ParseFloat(musicRateNumber.FindString(raw), 64)
	if strings.Contains(raw, "mbps") {
		n *= 1000
	} else if n >= 10000 {
		n /= 1000
	}
	return n
}

func musicSearchFormat(song model.Song) int {
	format := strings.ToLower(strings.TrimPrefix(song.Ext, "."))
	if format == "" {
		format = strings.ToLower(song.Extra["available_format"])
	}
	if format == "" {
		format = strings.ToLower(song.Extra["format"])
	}
	switch format {
	case "flac":
		return 3
	case "wav":
		return 2
	case "mp3":
		return 1
	}
	return 0
}

// Performer evidence is a tie breaker, not an originality certificate: credits
// in cover titles and agreement across sources help disambiguate same-name songs.
func musicPerformerEvidence(songs []model.Song) map[string]int {
	sources := make(map[string]map[string]bool)
	credits := make(map[string]bool)
	for _, song := range songs {
		if core.IsPreviewSong(song) {
			continue
		}
		title := musicBaseTitle(song.Name)
		for _, match := range musicCoverCredit.FindAllStringSubmatch(song.Name, -1) {
			for _, artist := range splitArtistTokens(match[1]) {
				credits[title+"|"+musicSearchText(artist)] = true
			}
		}
		derived := false
		for _, re := range musicVersionGroups {
			derived = derived || re.MatchString(song.Name+" "+song.Album)
		}
		if derived || song.Source == "" {
			continue
		}
		for _, artist := range splitArtistTokens(song.Artist) {
			key := title + "|" + musicSearchText(artist)
			if sources[key] == nil {
				sources[key] = make(map[string]bool)
			}
			sources[key][song.Source] = true
		}
	}
	evidence := make(map[string]int)
	for key, group := range sources {
		if len(group) >= 2 {
			evidence[key] = 1
		}
	}
	for key := range credits {
		evidence[key] += 2
	}
	return evidence
}

// rankMusicSearch preserves selectable full versions and IDs. Preview evidence
// excludes a candidate; relevance, performer and requested version precede bitrate.
func rankMusicSearch(query string, songs []model.Song) []model.Song {
	type ranked struct {
		song                              model.Song
		match, performer, version, format int
		rate                              float64
	}
	q := musicBaseTitle(query)
	items := make([]ranked, 0, len(songs))
	evidence := musicPerformerEvidence(songs)
	explicitVersion := false
	for _, re := range musicVersionGroups {
		explicitVersion = explicitVersion || re.MatchString(query)
	}
	for _, song := range songs {
		if core.IsPreviewSong(song) {
			continue
		}
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
		performer := 0
		for _, token := range splitArtistTokens(song.Artist) {
			performer = max(performer, evidence[title+"|"+musicSearchText(token)])
			// Explicit performer queries take precedence over inferred consensus.
			if normalized := musicSearchText(token); normalized != "" && (q == title+normalized || q == normalized+title) {
				score = max(score, 6)
			}
		}
		items = append(items, ranked{song, score, performer, version, musicSearchFormat(song), musicSearchRate(song)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.match != b.match {
			return a.match > b.match
		}
		if explicitVersion && a.version != b.version {
			return a.version < b.version
		}
		if a.performer != b.performer {
			return a.performer > b.performer
		}
		if a.version != b.version {
			return a.version < b.version
		}
		if a.format != b.format {
			return a.format > b.format
		}
		return a.rate > b.rate
	})
	result := make([]model.Song, len(items))
	for i, item := range items {
		result[i] = item.song
	}
	return result
}
