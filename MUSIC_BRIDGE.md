# MP 音乐下载桥接 API

该接口用于让 MP 插件查询 go-music-dl 的音源，并把单曲加入 go-music-dl 现有的持久下载队列。它不改动 MP 的原生搜索，也不接管整理流程。

## 启用与认证

在 go-music-dl 容器环境中设置 `MUSIC_BRIDGE_TOKEN` 为随机长令牌，并通过 HTTPS 或可信内网访问。令牌不应提交到仓库、写入公开日志或发送给不受信任的客户端。未设置令牌时，桥接路由返回 `404` 并保持关闭；缺失或错误的 Bearer 令牌返回 `401`。

默认 Web 前缀是 `/music`，因此地址为：

```text
/music/api/music-bridge/search
/music/api/music-bridge/downloads
/music/api/music-bridge/downloads/{request_id}
```

若启动参数配置了其他 `--base-path`，将 `/music` 替换成对应前缀。每个请求使用：

```text
Authorization: Bearer <MUSIC_BRIDGE_TOKEN>
```

## 搜索

```http
GET /music/api/music-bridge/search?q=晴天&sources=netease&limit=10
```

`q` 必填，最多 200 个字符。`sources` 可省略，默认 `kugou,migu,qq,netease`；显式指定时保留选择，每次最多 4 个有效搜索源。`limit` 默认 10，范围为 1–50。当前源码还支持 `kuwo`、`bilibili`、`fivesing`、`jamendo`、`joox`、`qianqian`、`soda` 和 `apple` 等搜索源，但 Apple 当前只提供试听，歌曲候选会被过滤。

响应只包含插件下载所需字段；`extra` 是 JSON 字符串，插件应将它作为字符串原样传入下载请求：

```json
{
  "songs": [
    {
      "id": "song-id",
      "source": "netease",
      "name": "晴天",
      "artist": "周杰伦",
      "album": "叶惠美",
      "cover": "https://example.invalid/cover.jpg",
      "extra": "{\"album_id\":\"album-id\"}"
    }
  ]
}
```

搜索并发调用选定 provider，截止时间 6 秒；截止时保留已成功完成的来源结果，没有成功来源才返回 `504`。provider 不接收 context，底层调用可能继续到各自超时（共享 HTTP 客户端为 2 分钟）；进程最多保留 16 个未结束调用，并在启动前原子预留整次请求的容量。容量不足返回 `503`，不启动部分搜索。连续超时仍可能暂时占满容量，稍后重试。

## 加入下载队列

```http
POST /music/api/music-bridge/downloads
Content-Type: application/json
```

```json
{
  "request_id": "uuid-or-client-request-key",
  "song": {
    "id": "song-id",
    "source": "netease",
    "name": "晴天",
    "artist": "周杰伦",
    "album": "叶惠美",
    "cover": "https://example.invalid/cover.jpg",
    "extra": "{\"album_id\":\"album-id\"}"
  }
}
```

`request_id` 长度为 1–115，只能含字母、数字、点、下划线和连字符。它应在一次下载请求中保持不变。`song.extra` 必须是 JSON 对象字符串；没有额外信息时可省略或传空字符串。接口返回 `202 Accepted` 和当前队列状态：

```json
{"request_id":"uuid-or-client-request-key","status":"waiting"}
```

相同 `request_id` 和相同歌曲（按 `source`、`id` 判断）会返回原任务状态，不会再次入队；同一 `request_id` 改传另一首歌，或歌曲已由另一个活动任务下载时，返回 `409 Conflict`。搜索源不支持下载、缺少歌曲 ID 或 extra 格式错误时返回 `400`。

下载设置、输出目录、文件名模板和元数据选项沿用 Web 设置。队列持久化在现有设置数据库中；HTTP 客户端收到 `202` 后即可结束请求，后续通过状态接口轮询。断开提交连接或客户端超时不会撤销已接受的任务。本桥接 API 没有取消端点，也不会声称能中止正在运行的 provider 下载。

## 查询状态

```http
GET /music/api/music-bridge/downloads/uuid-or-client-request-key
```

```json
{
  "request_id": "uuid-or-client-request-key",
  "status": "success",
  "path": "/home/appuser/data/downloads/周杰伦 - 晴天.mp3",
  "error": "",
  "warning": ""
}
```

状态值复用现有队列：`waiting`、`downloading`、`success`、`skipped`、`failed`、`canceled`。未知 ID 返回 `404`，并带 `status: "not_found"`、空 `path` 和 `warning`，以及错误说明。失败任务保留其错误信息；相同 `request_id` 再次提交会返回原失败状态，避免意外重下。需要重试时使用已有队列管理界面。

## 文件发布与 MP 监控

桥接提交调用现有下载队列。音频获取、封面/歌词处理和可用的元数据内嵌发生在落盘前；保存时先在目标目录写入隐藏的 `.musicdl-*.part` 临时文件，写入并同步完成后再 rename 为带真实音频扩展名的文件。MP 的音频文件扫描只会遇到最终文件名，不会把下载中的字节当作成品；未完成的文件不会以 `.mp3`、`.flac` 等扩展名出现。此行为沿用 `core/download.go`，没有另建下载 worker 或修改 MP 整理逻辑。

该接口本身不代表真实音源或 NAS 已验证。部署后应先用单首歌曲验证共享目录挂载、MP 整理及 Navidrome 播放链路。

## 下游默认排序

DL 网页默认保留后端综合排序。手动选择“按音质”仍沿用网页的码率排序；它是用户显式覆盖，不改变下载校验。`node --test tests/song-sort.test.cjs` 验证默认与显式排序行为。

## DL 服务端综合排序（2026-10-30）

歌曲搜索先按歌名／明确歌手匹配；明确要求的版本优先于推断歌手。未指定版本时，用 Cover 署名和跨源歌手一致性作为弱证据，再降低衍生版本优先级。之后按有依据的 FLAC ＞ WAV ＞ MP3 格式偏好、同格式码率比较，未知格式不按码率猜测。码率读取原生 `Song.Bitrate`，兼容旧 `extra.bitrate`。桥接在 limit 截取前排序；网页默认复用后端排序。歌单、专辑导入不改变曲目顺序。

默认降低 Live／现场、Cover／翻唱、伴奏／纯音乐、Remix／改编、Demo、DJ／变速及常见衍生版的优先级；明确查询对应版本时尊重该选择。明确试听、片段标记和 Apple 试听源排除；不单凭短时长删除完整短作品。原歌手判断是启发式，不做权威认证，版本仍可选择。

原唱依据来自输入歌手和音源版本元数据，不表示已通过权威数据库核验。当前音源不提供原唱资源时，仍会返回可用衍生版本，不把这些结果标为原唱。未增加额外详情探测请求，避免拖慢搜索；缺失音质不会猜测。

## 下载质量检查（2026-10-30）

Web 普通下载（含 Range、关闭标签嵌入）、队列和桥接统一在保存/返回前用 ffprobe 检查实际字节；在线播放继续支持上游流。必须可找到 ffprobe，缺失时明确失败。不会将有损文件转成 FLAC。

- 酷我改为高质量 FLAC、FLAC、320k、128k 请求顺序；咪咕按 FLAC、WAV、MP3 选择；网易云自动选择先 hires、lossless、exhigh，拒绝返回 freeTrialInfo 的试听链接。固定依赖和补丁见 `third_party/music-lib/LOCAL_PATCHES.md`。
- 已知完整时长通过 `extra.musicdl_duration` 随原候选传入；实际短于预期超过 5%（至少容差 5 秒）则拒绝。未知完整时长且实际不足 60 秒拒绝；已知完整短作品正常接受。
- FLAC、ALAC、常见无损/PCM 编码接受；MP3 至少 300 kbps（320k 容差），AAC/Vorbis/WMA 至少 256 kbps，Opus 至少 160 kbps。不能确认编码或码率时失败。码率门槛不等于对母带或历史转码的认证。
- 检查通过后 warning 提供实际编码、码率和时长。权限不足、音源不可用或质量不足报失败，不自动下载翻唱替代，不覆盖已有曲库。旧低质量文件不会自动升级或重新校验去重记录。

已有 MP 插件若显式填了 `netease`，需清空“音源”或改填 `kugou,migu,qq,netease` 才使用多源；无需为本次服务端调整升级插件。旧搜索卡片保留旧快照，更新后重新搜索。
