package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/music-lib/model"
	"gorm.io/gorm"
)

const (
	musicBridgeQueueRequestPrefix = "music-bridge:"
	musicBridgeDefaultSource      = "netease"
	musicBridgeDefaultLimit       = 10
	musicBridgeMaxLimit           = 50
	musicBridgeMaxSources         = 4
)

var (
	musicBridgeSearchTimeout = 6 * time.Second
	musicBridgeSearchSlots   = make(chan struct{}, musicBridgeMaxSources)
)

type musicBridgeSearchFunc func(source, keyword string) ([]model.Song, error)

type musicBridgeSongRequest struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Name   string `json:"name"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Cover  string `json:"cover"`
	Extra  string `json:"extra"`
}

type musicBridgeDownloadRequest struct {
	RequestID string                 `json:"request_id"`
	Song      musicBridgeSongRequest `json:"song"`
}

type musicBridgeSongResponse struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Name   string `json:"name"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Cover  string `json:"cover"`
	Extra  string `json:"extra"`
}

type musicBridgeSearchResult struct {
	index int
	songs []model.Song
	err   error
}

func RegisterMusicBridgeRoutes(api *gin.RouterGroup, queue *core.DownloadQueue) {
	registerMusicBridgeRoutes(api, queue, func(source, keyword string) ([]model.Song, error) {
		search := core.GetSearchFunc(source)
		if search == nil {
			return nil, errors.New("unsupported search source")
		}
		return search(keyword)
	})
}

func registerMusicBridgeRoutes(api *gin.RouterGroup, queue *core.DownloadQueue, search musicBridgeSearchFunc) {
	bridge := api.Group("/api/music-bridge")
	bridge.Use(musicBridgeBearerAuth())
	bridge.GET("/search", func(c *gin.Context) {
		keyword := strings.TrimSpace(c.Query("q"))
		if keyword == "" || len([]rune(keyword)) > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "q is required and must be at most 200 characters"})
			return
		}

		sources, err := musicBridgeSources(c.QueryArray("sources"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		limit := musicBridgeDefaultLimit
		if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
			limit, err = strconv.Atoi(rawLimit)
			if err != nil || limit < 1 || limit > musicBridgeMaxLimit {
				c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 50"})
				return
			}
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), musicBridgeSearchTimeout)
		defer cancel()
		songs, err := musicBridgeSearchSongs(ctx, search, keyword, sources)
		if err != nil {
			switch {
			case errors.Is(err, errMusicBridgeSearchBusy):
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "search capacity is busy; retry later"})
			case errors.Is(err, context.DeadlineExceeded):
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": "search timed out; the provider call may still be running"})
			case errors.Is(err, context.Canceled):
				return
			default:
				c.JSON(http.StatusBadGateway, gin.H{"error": "all selected search sources failed"})
			}
			return
		}
		songs = rankMusicSearch(keyword, songs)
		if len(songs) > limit {
			songs = songs[:limit]
		}
		response := make([]musicBridgeSongResponse, 0, len(songs))
		for _, song := range songs {
			extra, marshalErr := json.Marshal(song.Extra)
			if marshalErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode search results"})
				return
			}
			if len(song.Extra) == 0 {
				extra = []byte("{}")
			}
			response = append(response, musicBridgeSongResponse{
				ID: song.ID, Source: song.Source, Name: song.Name, Artist: song.Artist,
				Album: song.Album, Cover: song.Cover, Extra: string(extra),
			})
		}
		c.JSON(http.StatusOK, gin.H{"songs": response})
	})

	bridge.POST("/downloads", func(c *gin.Context) {
		if queue == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "download queue is unavailable"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var request musicBridgeDownloadRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		request.RequestID = strings.TrimSpace(request.RequestID)
		request.Song.ID = strings.TrimSpace(request.Song.ID)
		request.Song.Source = strings.TrimSpace(request.Song.Source)
		if !validMusicBridgeRequestID(request.RequestID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "request_id must contain 1-115 letters, digits, dots, underscores, or hyphens"})
			return
		}
		if request.Song.ID == "" || request.Song.Source == "" || core.GetDownloadFunc(request.Song.Source) == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "song requires a supported id and source"})
			return
		}
		extra, err := parseMusicBridgeExtra(request.Song.Extra)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "song.extra must be a JSON object string"})
			return
		}

		settings := core.GetWebSettings()
		song := model.Song{
			ID: request.Song.ID, Source: request.Song.Source, Name: request.Song.Name,
			Artist: request.Song.Artist, Album: request.Song.Album, Cover: request.Song.Cover,
			Extra: extra,
		}
		queueRequestID := musicBridgeQueueRequestPrefix + request.RequestID
		input := core.DownloadTaskInput{
			Song: song, Directory: core.DownloadDirOrDefault(settings.DownloadDir),
			FilenameTemplate: settings.DownloadFilenameTemplate, Embed: settings.EmbedDownload,
		}
		if _, err := queue.Enqueue(queueRequestID, []core.DownloadTaskInput{input}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue download"})
			return
		}
		task, err := queue.FindByRequestKey(musicBridgeRequestKey(request.RequestID))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusConflict, gin.H{"error": "song is already queued under another request_id"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read queued download"})
			return
		}
		var original core.DownloadTaskInput
		if err := json.Unmarshal([]byte(task.Payload), &original); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read queued download"})
			return
		}
		if original.Song.ID != song.ID || original.Song.Source != song.Source {
			c.JSON(http.StatusConflict, gin.H{"error": "request_id was already used for a different song"})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"request_id": request.RequestID, "status": task.Status})
	})

	bridge.GET("/downloads/:request_id", func(c *gin.Context) {
		requestID := c.Param("request_id")
		if !validMusicBridgeRequestID(requestID) {
			writeMusicBridgeNotFound(c, requestID)
			return
		}
		if queue == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "download queue is unavailable"})
			return
		}
		task, err := queue.FindByRequestKey(musicBridgeRequestKey(requestID))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeMusicBridgeNotFound(c, requestID)
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read queued download"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"request_id": requestID,
			"status":     task.Status,
			"path":       task.Path,
			"error":      task.Error,
			"warning":    task.Warning,
		})
	})
}

var errMusicBridgeSearchBusy = errors.New("music bridge search capacity is busy")

func musicBridgeBearerAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := strings.TrimSpace(os.Getenv("MUSIC_BRIDGE_TOKEN"))
		if expected == "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !musicBridgeTokenEqual(parts[1], expected) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

func musicBridgeTokenEqual(got, expected string) bool {
	gotDigest := sha256.Sum256([]byte(got))
	expectedDigest := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(gotDigest[:], expectedDigest[:]) == 1
}

func musicBridgeSources(queryValues []string) ([]string, error) {
	var sources []string
	seen := make(map[string]struct{})
	for _, value := range queryValues {
		for _, source := range strings.Split(value, ",") {
			source = strings.TrimSpace(source)
			if source == "" {
				continue
			}
			if core.GetSearchFunc(source) == nil {
				return nil, errors.New("unsupported search source: " + source)
			}
			if _, ok := seen[source]; ok {
				continue
			}
			seen[source] = struct{}{}
			sources = append(sources, source)
		}
	}
	if len(sources) == 0 {
		sources = []string{musicBridgeDefaultSource}
	}
	if len(sources) > musicBridgeMaxSources {
		return nil, errors.New("at most four search sources may be selected")
	}
	return sources, nil
}

func musicBridgeSearchSongs(ctx context.Context, search musicBridgeSearchFunc, keyword string, sources []string) ([]model.Song, error) {
	results := make(chan musicBridgeSearchResult, len(sources))
	started := 0
	for index, source := range sources {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case musicBridgeSearchSlots <- struct{}{}:
			started++
			go func(index int, source string) {
				defer func() { <-musicBridgeSearchSlots }()
				songs, err := search(source, keyword)
				results <- musicBridgeSearchResult{index: index, songs: songs, err: err}
			}(index, source)
		default:
			return nil, errMusicBridgeSearchBusy
		}
	}

	ordered := make([][]model.Song, len(sources))
	failed := 0
	for range started {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-results:
			if result.err != nil {
				failed++
				continue
			}
			ordered[result.index] = result.songs
		}
	}
	if failed == started {
		return nil, errors.New("all selected search sources failed")
	}

	var songs []model.Song
	for index, group := range ordered {
		for _, song := range group {
			if strings.TrimSpace(song.ID) == "" {
				continue
			}
			song.Source = sources[index]
			songs = append(songs, song)
		}
	}
	return songs, nil
}

func validMusicBridgeRequestID(requestID string) bool {
	if requestID == "" || len(requestID) > 115 {
		return false
	}
	for _, r := range requestID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func musicBridgeRequestKey(requestID string) string {
	return musicBridgeQueueRequestPrefix + requestID + ":0"
}

func parseMusicBridgeExtra(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var extra map[string]string
	if err := json.Unmarshal([]byte(raw), &extra); err != nil || extra == nil {
		return nil, errors.New("invalid extra")
	}
	return extra, nil
}

func writeMusicBridgeNotFound(c *gin.Context, requestID string) {
	c.JSON(http.StatusNotFound, gin.H{
		"request_id": requestID,
		"status":     "not_found",
		"path":       "",
		"error":      "download request not found",
		"warning":    "",
	})
}
