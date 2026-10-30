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

`q` 必填，最多 200 个字符。`sources` 可省略（默认只搜 `netease`），也可用逗号分隔或重复传入；每次最多 4 个有效搜索源。`limit` 可选，默认 10，范围为 1–50。当前源码中的搜索源为 `netease`、`qq`、`kugou`、`kuwo`、`migu`、`bilibili`、`fivesing`、`jamendo`、`joox`、`qianqian`、`soda` 和 `apple`。

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

搜索对每个来源并发调用现有 provider，整体截止时间为 6 秒。provider 接口本身不接收 context，因此超时或客户端断开后，底层调用无法被强制取消；服务进程最多保留 4 个未结束的搜索调用，容量已满时返回 `503`，整体搜索超时返回 `504`。选择单一来源（默认 `netease`）可减少外部请求和等待时间。

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

DL 网页歌曲列表默认使用既有 quality 排序并降序展示；码率未知按 0 放后，同码率保留原顺序。`node --test tests/song-sort.test.cjs` 验证默认排序行为。

## DL 服务端综合排序（2026-10-30）

歌曲搜索统一在 DL 后端按歌名／歌手匹配度、版本提示、已知码率排序；桥接 API 在截取 limit 前排序，MP 插件无需升级，仍最多展示 10 条。网页关键词搜歌复用相同规则，默认综合排序；手动按音质仍可用。歌单、专辑导入不改曲目顺序。

默认降低 Live／现场、Cover／翻唱、伴奏／纯音乐、Remix／改编、Demo 的优先级；查询明确包含对应版本时改为优先匹配该版本。版本不删除，避免误杀。歌名和歌手支持两种前后顺序及连写。缺少码率时排在同等匹配的已知码率之后。

原唱依据来自输入歌手和音源版本元数据，不表示已通过权威数据库核验。当前音源不提供原唱资源时，仍会返回可用衍生版本，不把这些结果标为原唱。未增加额外详情探测请求，避免拖慢搜索；缺失音质不会猜测。
