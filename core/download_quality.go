package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/guohuiyuan/music-lib/model"
)

type audioInspection struct {
	Codec    string
	Duration float64
	Bitrate  int
}

// Search quality is a catalogue claim. Inspect the received bytes before writing
// an audio file so previews, malformed responses and low-rate fallbacks fail.
func checkDownloadedAudio(song model.Song, data []byte) (string, error) {
	probe, err := ResolveFFprobePath()
	if err != nil {
		return "", fmt.Errorf("无法核验下载音质：需要 ffprobe：%w", err)
	}
	file, err := os.CreateTemp("", "music-dl-audio-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,bit_rate,duration:format=duration,bit_rate", "-of", "json", file.Name())
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("下载文件音频核验失败：格式无效或 ffprobe 无法读取")
	}
	var response struct {
		Streams []struct {
			Codec    string `json:"codec_name"`
			Bitrate  string `json:"bit_rate"`
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			Bitrate  string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &response); err != nil || len(response.Streams) == 0 {
		return "", fmt.Errorf("下载文件不包含可验证的音轨")
	}
	stream := response.Streams[0]
	duration, _ := strconv.ParseFloat(stream.Duration, 64)
	if duration <= 0 {
		duration, _ = strconv.ParseFloat(response.Format.Duration, 64)
	}
	bitrate, _ := strconv.Atoi(stream.Bitrate)
	if bitrate <= 0 {
		bitrate, _ = strconv.Atoi(response.Format.Bitrate)
	}
	audio := audioInspection{Codec: stream.Codec, Duration: duration, Bitrate: bitrate}
	if err := validateAudioInspection(song, audio); err != nil {
		return "", err
	}
	return fmt.Sprintf("音频已核验：%s，%d kbps，%.0f 秒", strings.ToUpper(audio.Codec), audio.Bitrate/1000, audio.Duration), nil
}

func validateAudioInspection(song model.Song, audio audioInspection) error {
	if IsPreviewSong(song) {
		return fmt.Errorf("拒绝试听或短片段，请选择完整歌曲版本")
	}
	expected := song.Duration
	if expected <= 0 {
		expected, _ = strconv.Atoi(song.Extra["musicdl_duration"])
	}
	if math.IsNaN(audio.Duration) || math.IsInf(audio.Duration, 0) || audio.Duration <= 0 || (audio.Duration < 60 && expected <= 0) {
		return fmt.Errorf("下载音频时长无效或不足 60 秒且缺少完整时长信息，疑似试听片段")
	}
	if expected > 0 && audio.Duration < float64(expected)-math.Max(5, float64(expected)*0.05) {
		return fmt.Errorf("下载音频不完整：实际 %.0f 秒，预期 %d 秒；可能受试听或会员权限限制", audio.Duration, expected)
	}
	minimum := 0
	switch audio.Codec {
	case "flac", "alac", "wavpack", "ape", "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_s16be", "pcm_s24be", "pcm_s32be", "pcm_f32le", "pcm_f64le":
		return nil
	case "mp3":
		minimum = 300000 // Allow frame/average reporting tolerance for nominal 320k.
	case "aac", "vorbis", "wmav2", "wmapro":
		minimum = 256000
	case "opus":
		minimum = 160000
	default:
		return fmt.Errorf("无法确认下载音频质量：不支持的编码 %s", audio.Codec)
	}
	if audio.Bitrate < minimum {
		return fmt.Errorf("下载音质不足：%s 实际 %d kbps，要求至少 %d kbps；请检查会员登录状态或选择其他音源", audio.Codec, audio.Bitrate/1000, minimum/1000)
	}
	return nil
}
