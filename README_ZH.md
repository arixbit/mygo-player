[English](README.md) | 中文文档

# mygo-player

面向 [MyGo](https://github.com/egoist/mygo)（`v0.10.0`）的独立原生 UI 视频播放器插件，底层基于 **libmpv**（LGPL 2.1+），**纯 Go 零 cgo**（`CGO_ENABLED=0`）。

`mygo-player` 采用与 MyGo 官方原生插件一致的架构（内置 `mygo-plugin.json`、通过 `mygo/ffi` 动态加载，提供 `Load`、`LibraryPath`、`New`、`View` 等简洁接口）。视频画面直接绘制在 MyGo 的原生 UI 图层中，而不是覆盖在最顶层的系统子窗口，因此播放控制栏、弹幕、圆角裁切、弹出菜单等 UI 元素始终都能自然叠在视频上方，不会出现遮挡。

## 为什么选择 `mygo-player`？与内置 `ui/video` 的区别

MyGo 内置的 `ui/video` 直接调用各操作系统的原生媒体框架（AVFoundation、Media Foundation、GStreamer 和浏览器 `<video>`），不需要附带额外动态库，非常适合播放轻量的 MP4/H.264 片段或网页端应用。如果你的桌面应用需要更完整的播放器能力，`mygo-player` 正好作为它的补充：

| 对比项 | MyGo 内置 `ui/video` | `mygo-player` |
| --- | --- | --- |
| 播放内核 | 系统自带媒体框架（AVFoundation / Media Foundation / GStreamer / `<video>`） | 内嵌 `libmpv`（LGPL 2.1+），通过 `mygo/ffi` 加载，零 cgo |
| 额外动态库 | 无 | `libmpv` 动态库（通过 `mygo-plugin.json` 自动管理与打包） |
| 视频封装与编码 | 全平台支持 MP4 / MOV（H.264 / AAC）；HEVC / VP9 / AV1 取决于系统解码器 | 开箱即用支持主流格式（MKV、MP4、WebM、TS、FLV；H.264、HEVC、AV1、VP9 等） |
| 多音轨切换 | 未开放接口 | 运行时查看音轨列表、自由切换音轨或关闭音频 |
| 字幕支持 | 未开放接口 | 同时支持视频内嵌字幕与外挂字幕（SRT、ASS/SSA、VTT、SUP 等），支持 libass 渲染及显隐切换 |
| 在线流与自定义请求头 | 支持普通 URL（`OpenURL`） | 支持按每次播放设置自定义 HTTP 请求头（鉴权 Token、Cookie、防盗链 `Referer` 与 `User-Agent`），每次 `Open()` 自动隔离重置 |
| 硬件解码控制 | 由系统自动决定 | 支持运行时配置与动态切换（VideoToolbox、D3D11VA、VA-API 或软解） |
| UI 叠层渲染 | 原生 UI 图层合成 | 原生 UI 图层合成（控制栏、弹幕、弹窗、圆角始终清晰叠在视频上方） |
| 适用场景 | 宣传片、引导动画、轻量视频展示、浏览器/WASM 应用 | 专业媒体播放器、番剧/影视客户端、NAS/Emby/Jellyfin 客户端、带鉴权的在线视频播放 |

## 平台支持

| 平台 | 渲染方式 | 默认硬解模式 | 音频输出 | `mygo-plugin.json` 预构建 / 构建脚本 |
| --- | --- | --- | --- | --- |
| `darwin-arm64` | 零拷贝 GPU 渲染（`IOSurface` -> Metal `*native.Texture`） | `auto-safe`（VideoToolbox） | CoreAudio | 已发布（`scripts/build-libmpv-macos.sh`） |
| `darwin-amd64` | 零拷贝 GPU 渲染（`IOSurface` -> Metal `*native.Texture`） | `auto-safe`（VideoToolbox） | CoreAudio | 已发布（`scripts/build-libmpv-macos.sh`） |
| `windows-amd64` | 双缓冲 `*ui.Bitmap` 图层合成（`MPV_RENDER_API_TYPE_SW`，无子窗口 HWND 遮挡） | `auto-copy`（D3D11VA） | WASAPI | 已发布（`scripts/build-libmpv-windows.sh`） |
| `linux-amd64` | 双缓冲 `*ui.Bitmap` 图层合成（`MPV_RENDER_API_TYPE_SW`） | `auto-copy`（VA-API / 自动回退软解） | PulseAudio（兼容 `pipewire-pulse`）/ ALSA | 已发布（`scripts/build-libmpv-linux.sh`） |
| `linux-arm64` | 双缓冲 `*ui.Bitmap` 图层合成（`MPV_RENDER_API_TYPE_SW`） | `auto-copy`（回读硬解 / 自动回退软解） | PulseAudio（兼容 `pipewire-pulse`）/ ALSA | 已发布（`scripts/build-libmpv-linux.sh`） |

> **其他平台（`js/wasm`、iOS、Android）：** 编译时不引入桌面 `purego` / 渲染依赖（`js/wasm` 支持零 cgo 直接编译），运行时调用会返回标准错误 `errors.ErrUnsupported`，方便跨平台项目安全引入。

## 安装

```bash
go get github.com/arixbit/mygo-player
```

- 需要 **Go** 与 **MyGo（`v0.10.0`）**，全程 **`CGO_ENABLED=0`**，无需安装 C 编译器。
- 包内已嵌入 `mygo-plugin.json`，使用 `mygo dev` 和 `mygo build` 时会自动发现并打包对应的 `libmpv` 动态库。

## 快速开始

创建播放器时可以把 `HWDec` 留空，插件会自动选择各平台的推荐默认值（macOS 使用 `auto-safe`，Windows 和 Linux 使用 `auto-copy`）：

```go
package main

import (
	"log"

	player "github.com/arixbit/mygo-player"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.WhenReady(func() {
		p, err := player.New(player.Options{})
		if err != nil {
			log.Fatal(err)
		}
		_ = p.Open("/path/to/movie.mkv", player.OpenOptions{
			Subtitles: []string{"/path/to/movie.srt"},
		})

		win := mygo.NewWindow(mygo.WindowOptions{
			Title:  "Player",
			Width:  1024,
			Height: 600,
			Content: ui.View(func(c *ui.Context) {
				player.View(c, p).Fill()
			}),
		})
		win.OnClosed(func() { _ = p.Close() })
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
```

### 运行示例播放器

```bash
# 自动使用 mygo-plugin.json 声明的预构建 libmpv，或本机已安装的 libmpv：
CGO_ENABLED=0 go run ./examples/player /path/to/video.mkv

# 或者手动指定 libmpv 动态库路径（.dylib / .dll / .so）：
MYGO_NATIVE_MPV=/path/to/libmpv.dylib CGO_ENABLED=0 go run ./examples/player \
  -sub /path/to/subtitle.srt \
  /path/to/video.mkv
```

`examples/player` 内置快捷键：
- `Space`：播放 / 暂停
- `Left` / `Right`：快退 5 秒 / 快进 5 秒
- `M`：静音 / 取消静音
- `Cmd+O`（macOS）/ `Ctrl+O`（Windows 与 Linux）：打开本地视频文件

## 音轨切换、字幕与在线流（自定义请求头）

- **在线流自定义 HTTP 请求头**：播放需要登录或防盗链校验的在线视频时，可以直接传入鉴权 Token（`Authorization`）、`Cookie`，以及防盗链所需的 `Referer` 和 `User-Agent`。每次调用 `Open()` 传入的请求头都会自动隔离，下一次打开其他视频时自动重置，不会把上一个视频的 Token 或 Cookie 泄露出去。
- **多音轨切换**：通过 `AudioTracks()` 获取视频内所有音轨，用 `SelectAudioTrack` 随时切换配音语言或评论音轨，也可用 `DisableAudioTrack` 关闭音轨。
- **内嵌与外挂字幕**：同时支持读取视频内嵌字幕和挂载外部字幕文件（如 `.srt`、`.ass`、`.ssa`、`.vtt`、`.sup`），使用 `AddSubtitleFile` 即可加载并激活，配合 `SetSubtitleVisible` 随时显示或隐藏。
- **运行时硬解控制**：随时通过 `SetHWDec` 切换解码模式（`HWDecAutoSafe`、`HWDecAutoCopy`、`HWDecVideoToolbox`、`HWDecD3D11VACopy`、`HWDecVAAPICopy`、`HWDecNone`），并通过 `CurrentHWDec()` 查看当前实际生效的硬解后端。

```go
// 播放在线视频流，附带鉴权 Token、Cookie 以及防盗链 Referer / User-Agent。
err := p.Open("https://media.example.com/episode.mkv", player.OpenOptions{
	Headers: map[string]string{
		"Authorization": "Bearer your-token",
		"Cookie":        "session=abc",
	},
	UserAgent: "MyApp/1.0",
	Referrer:  "https://media.example.com/",
})

// 切换多音轨。
if audios := p.AudioTracks(); len(audios) > 1 {
	_ = p.SelectAudioTrack(audios[1].ID)
}

// 加载外挂字幕（支持 SRT / ASS / VTT / SUP 等）并控制显隐。
_ = p.AddSubtitleFile("/path/to/chinese.ass", player.SubtitleSelect)
_ = p.SetSubtitleVisible(true)

// 运行时切换或查询硬件解码状态。
_ = p.SetHWDec(player.HWDecAutoCopy)
log.Println("当前硬解后端:", p.CurrentHWDec())
```

## 无头 UI 测试（`ui.NewTester`）

因为 `player.View` 直接渲染在 MyGo 的原生 UI 图层中，你可以直接使用 MyGo 的无头测试工具 `ui.NewTester` 来验证播放界面布局、叠层控制栏、空状态/空闲状态以及高分屏视口尺寸——无需弹出真实窗口，甚至不需要本机安装 `libmpv`：

```go
func TestPlayerOverlay(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Children(func() {
			player.View(c, nil).Fill()
			ui.Text(c, "已暂停").Absolute().Left(16).Top(16)
		})
	}, 320, 180)
	tt.Frame()

	if !tt.HasText("已暂停") {
		t.Fatal("期望叠层文字正确绘制在 player.View 上方")
	}
}
```

在本地运行测试：

```bash
# 快速运行无头 UI 与单元测试（无需安装 libmpv）：
CGO_ENABLED=0 go test -v -run 'TestView|TestManifest|TestSplitHTTP|TestParseTrack|TestSWRenderer' ./...

# 运行完整测试（包含真实 MKV 播放、音轨/字幕切换与请求头集成测试，需要 libmpv + ffmpeg）：
CGO_ENABLED=0 MYGO_REQUIRE_LIBMPV=1 go test -v ./...
```

## 原生库查找顺序与 LGPL 构建

`mygo-player` 内置 `mygo-plugin.json`，通过 `mygo/ffi` 按以下优先级查找 `libmpv` 动态库：

1. 代码中调用 `player.SetLibraryPath(...)` 或环境变量 `$MYGO_NATIVE_MPV`。
2. 打包后的应用资源目录（macOS 的 `Contents/Resources/libmpv.dylib` 或 `Contents/Frameworks/`；Windows 可执行文件同级的 `mpv-2.dll`；Linux 可执行文件同级或应用附带目录中的 `libmpv.so`）。
3. 未打包开发运行时，项目本地的 `.mygo/native/<goos>-<goarch>/` 目录。
4. `mygo-plugin.json` 中声明的带 SHA-256 校验的预构建产物（未打包运行时自动下载一次到用户缓存；执行 `mygo build` 时自动打入安装包）。
5. 本地开发回退路径（macOS 的 Homebrew `/opt/homebrew/lib/libmpv.dylib` 或 `/usr/local/lib/libmpv.dylib`；Windows 当前目录或可执行文件目录的 `mpv-2.dll`；Linux 系统库路径中的 `libmpv.so` / `libmpv.so.2`）。

如需在本地或 GitHub Actions 中构建自包含的 LGPL-2.1+ 动态库：
- **macOS**：`./scripts/build-libmpv-macos.sh arm64 dist/libmpv-darwin-arm64.dylib`
- **Windows（MSYS2 UCRT64）**：`./scripts/build-libmpv-windows.sh amd64 dist/mpv-2.dll`
- **Linux**：`./scripts/build-libmpv-linux.sh amd64 dist/libmpv-linux-amd64.so`
- **GitHub Actions**：`.github/workflows/build-libmpv.yml` 自动构建 `darwin-arm64`、`darwin-amd64`、`windows-amd64`、`linux-amd64` 和 `linux-arm64` 的 LGPL-2.1+ 动态库，校验动态依赖纯净性，运行零 cgo 的 Go 集成测试，上传带 SHA-256 校验的 Release 资源并更新 `mygo-plugin.json`。

## 许可证

- Go 插件源码、示例与构建脚本采用 **MIT License**（见 [LICENSE](LICENSE)）。
- 底层 `libmpv` 动态库采用 **LGPL 2.1 或更高版本**（构建时严格排除所有 GPL 与 nonfree 组件）。完整的第三方依赖清单与 LGPL 动态替换指引请参阅 [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md)。
