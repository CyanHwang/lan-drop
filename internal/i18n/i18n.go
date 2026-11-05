// Package i18n holds the translation catalogue shared by the desktop app, the
// embedded browser UI and the headless server. Two languages are supported:
// English and Simplified Chinese.
package i18n

import (
	"fmt"
	"strings"
)

// Lang is a concrete, supported language tag.
type Lang string

const (
	EN   Lang = "en"
	ZHCN Lang = "zh-CN"
)

// Supported lists the languages the UI can render, in menu order.
var Supported = []Lang{EN, ZHCN}

// Msg is a translation key. Using a named type keeps typos out of call sites.
type Msg string

// Error messages surfaced through the HTTP API and the desktop UI.
const (
	ErrServerRunning     Msg = "err.serverRunning"
	ErrNoShares          Msg = "err.noShares"
	ErrSharePathInvalid  Msg = "err.sharePathInvalid"
	ErrShareInaccessible Msg = "err.shareInaccessible"
	ErrShareNotFolder    Msg = "err.shareNotFolder"
	ErrNoUsableShares    Msg = "err.noUsableShares"
	ErrListen            Msg = "err.listen"
	ErrBadPath           Msg = "err.badPath"
	ErrShareNotFound     Msg = "err.shareNotFound"
	ErrUnknownEndpoint   Msg = "err.unknownEndpoint"
	ErrDirNotFound       Msg = "err.dirNotFound"
	ErrNotFolder         Msg = "err.notFolder"
	ErrMissingPath       Msg = "err.missingPath"
	ErrFileNotFound      Msg = "err.fileNotFound"
	ErrTargetIsFolder    Msg = "err.targetIsFolder"
	ErrInvalidName       Msg = "err.invalidName"
	ErrReadOnly          Msg = "err.readOnly"
	ErrDeleteRoot        Msg = "err.deleteRoot"
	ErrNotFound          Msg = "err.notFound"
	ErrOffsetMismatch    Msg = "err.offsetMismatch"
	ErrAppNotReady       Msg = "err.appNotReady"
	ErrClipboard         Msg = "err.clipboard"
	ErrServiceNotRunning Msg = "err.serviceNotRunning"
	ErrInvalidPath       Msg = "err.invalidPath"
	ErrConfigWrite       Msg = "err.configWrite"
)

// Desktop shell strings.
const (
	AppDescription   Msg = "app.description"
	TrayTooltip      Msg = "tray.tooltip"
	TrayOpen         Msg = "tray.open"
	TrayQuit         Msg = "tray.quit"
	DialogPickFolder Msg = "dialog.pickFolder"
)

// Headless server strings.
const (
	CliUsageTitle    Msg = "cli.usageTitle"
	CliUsageLine     Msg = "cli.usageLine"
	CliOptsTitle     Msg = "cli.optsTitle"
	CliOptAddr       Msg = "cli.optAddr"
	CliOptShare      Msg = "cli.optShare"
	CliOptConfig     Msg = "cli.optConfig"
	CliOptReadOnly   Msg = "cli.optReadOnly"
	CliOptAutoPort   Msg = "cli.optAutoPort"
	CliExample       Msg = "cli.example"
	CliNoShares      Msg = "cli.noShares"
	CliBadAddr       Msg = "cli.badAddr"
	CliBannerTitle   Msg = "cli.bannerTitle"
	CliModeReadOnly  Msg = "cli.modeReadOnly"
	CliModeReadWrite Msg = "cli.modeReadWrite"
	CliShares        Msg = "cli.shares"
	CliURLs          Msg = "cli.urls"
	CliStopHint      Msg = "cli.stopHint"
	CliStopping      Msg = "cli.stopping"
	CliStopError     Msg = "cli.stopError"
)

var catalogue = map[Lang]map[Msg]string{
	EN: {
		ErrServerRunning:     "server is already running",
		ErrNoShares:          "at least one shared folder is required",
		ErrSharePathInvalid:  "%s: invalid path",
		ErrShareInaccessible: "%s: folder is not accessible",
		ErrShareNotFolder:    "%s: not a folder",
		ErrNoUsableShares:    "no usable shared folders",
		ErrListen:            "cannot bind port: %v",
		ErrBadPath:           "invalid path",
		ErrShareNotFound:     "shared folder not found",
		ErrUnknownEndpoint:   "unknown endpoint",
		ErrDirNotFound:       "folder does not exist",
		ErrNotFolder:         "not a folder",
		ErrMissingPath:       "missing path parameter",
		ErrFileNotFound:      "file does not exist",
		ErrTargetIsFolder:    "target is a folder",
		ErrInvalidName:       "invalid file name",
		ErrReadOnly:          "server is in read-only mode",
		ErrDeleteRoot:        "refusing to delete the share root",
		ErrNotFound:          "not found",
		ErrOffsetMismatch:    "offset mismatch",
		ErrAppNotReady:       "application is not ready",
		ErrClipboard:         "failed to write to the clipboard",
		ErrServiceNotRunning: "service is not running",
		ErrInvalidPath:       "invalid path: %v",
		ErrConfigWrite:       "cannot save configuration: %v",

		AppDescription:   "LAN file transfer",
		TrayTooltip:      "LanDrop - LAN file transfer",
		TrayOpen:         "Open LanDrop",
		TrayQuit:         "Quit LanDrop",
		DialogPickFolder: "Choose a folder to share",

		CliUsageTitle:    "LanDrop server mode - headless HTTP file service",
		CliUsageLine:     "Usage:\n  %s [options]",
		CliOptsTitle:     "Options:",
		CliOptAddr:       "listen address, e.g. :57577, 0.0.0.0:9000, 127.0.0.1:57577",
		CliOptShare:      "shared folder; repeat the flag or separate with commas (overrides the config file)",
		CliOptConfig:     "config file path (default: config.ini next to the executable)",
		CliOptReadOnly:   "read-only mode: reject uploads, mkdir and delete",
		CliOptAutoPort:   "scan forward when the port is taken (default: fail loudly)",
		CliExample:       "Examples:\n  %s -share /data/photos -share /data/docs\n  %s -addr 0.0.0.0:9000 -config /etc/landrop/config.ini -readonly",
		CliNoShares:      "error: no shared folders.\n  Pass -share /path/to/dir, or add one to config.ini and retry.",
		CliBadAddr:       "error: invalid listen address %q: %v",
		CliBannerTitle:   "LanDrop is running (%s)",
		CliModeReadOnly:  "read-only",
		CliModeReadWrite: "read-write",
		CliShares:        "Shared folders (%d):",
		CliURLs:          "Reachable at:",
		CliStopHint:      "Press Ctrl+C to stop.",
		CliStopping:      "Stopping...",
		CliStopError:     "error while stopping: %v",
	},
	ZHCN: {
		ErrServerRunning:     "服务已在运行",
		ErrNoShares:          "至少需要添加一个共享目录",
		ErrSharePathInvalid:  "%s：路径无效",
		ErrShareInaccessible: "%s：目录不可访问",
		ErrShareNotFolder:    "%s：不是文件夹",
		ErrNoUsableShares:    "没有可用的共享目录",
		ErrListen:            "无法监听端口: %v",
		ErrBadPath:           "非法路径",
		ErrShareNotFound:     "共享目录不存在",
		ErrUnknownEndpoint:   "未知接口",
		ErrDirNotFound:       "目录不存在",
		ErrNotFolder:         "不是文件夹",
		ErrMissingPath:       "缺少 path 参数",
		ErrFileNotFound:      "文件不存在",
		ErrTargetIsFolder:    "目标是文件夹",
		ErrInvalidName:       "文件名无效",
		ErrReadOnly:          "服务处于只读模式",
		ErrDeleteRoot:        "拒绝删除共享根目录",
		ErrNotFound:          "不存在",
		ErrOffsetMismatch:    "偏移量不匹配",
		ErrAppNotReady:       "应用未就绪",
		ErrClipboard:         "写入剪贴板失败",
		ErrServiceNotRunning: "服务未启动",
		ErrInvalidPath:       "路径无效: %v",
		ErrConfigWrite:       "无法保存配置: %v",

		AppDescription:   "局域网快传",
		TrayTooltip:      "LanDrop - 局域网快传",
		TrayOpen:         "打开 LanDrop",
		TrayQuit:         "退出 LanDrop",
		DialogPickFolder: "选择要共享的文件夹",

		CliUsageTitle:    "LanDrop 服务端模式 — 无界面，只提供局域网 HTTP 文件服务",
		CliUsageLine:     "用法：\n  %s [选项]",
		CliOptsTitle:     "选项：",
		CliOptAddr:       "监听地址，例如 :57577、0.0.0.0:9000、127.0.0.1:57577",
		CliOptShare:      "共享目录，可重复指定或用逗号分隔；指定后忽略配置文件",
		CliOptConfig:     "配置文件路径，默认为可执行文件同目录的 config.ini",
		CliOptReadOnly:   "只读模式：禁止上传、新建文件夹和删除",
		CliOptAutoPort:   "端口被占用时自动向后寻找可用端口（默认直接报错）",
		CliExample:       "示例：\n  %s -share /data/photos -share /data/docs\n  %s -addr 0.0.0.0:9000 -config /etc/landrop/config.ini -readonly",
		CliNoShares:      "错误：没有可用的共享目录。\n  用 -share /path/to/dir 指定，或在 config.ini 中配置后重试。",
		CliBadAddr:       "错误：无效的监听地址 %q：%v",
		CliBannerTitle:   "LanDrop 服务已启动（%s模式）",
		CliModeReadOnly:  "只读",
		CliModeReadWrite: "读写",
		CliShares:        "共享目录 %d 个：",
		CliURLs:          "访问地址：",
		CliStopHint:      "按 Ctrl+C 停止。",
		CliStopping:      "正在停止…",
		CliStopError:     "停止时出错：%v",
	},
}

// T renders a message in lang. Unknown keys return the key itself so a missing
// translation is visible instead of silently blanking the UI.
func T(lang Lang, m Msg, args ...any) string {
	text, ok := catalogue[lang][m]
	if !ok {
		text, ok = catalogue[EN][m]
	}
	if !ok {
		return string(m)
	}
	if len(args) == 0 {
		return text
	}
	return fmt.Sprintf(text, args...)
}

// Normalize maps an arbitrary BCP-47-ish tag ("zh", "zh-Hans", "en-US",
// "zh_CN.UTF-8") onto a supported language.
func Normalize(tag string) (Lang, bool) {
	t := strings.ToLower(strings.TrimSpace(tag))
	if t == "" {
		return "", false
	}
	// Strip POSIX locale suffixes: zh_CN.UTF-8 -> zh_cn
	if i := strings.IndexAny(t, ".@"); i >= 0 {
		t = t[:i]
	}
	t = strings.ReplaceAll(t, "_", "-")

	switch {
	case strings.HasPrefix(t, "zh"):
		// Traditional Chinese is closer to Simplified than to English for the
		// handful of strings this app ships.
		return ZHCN, true
	case strings.HasPrefix(t, "en"):
		return EN, true
	}
	return "", false
}

// Resolve turns a stored value into a concrete language. Anything missing or
// unrecognised falls back to the host OS language, which is what a first run
// or a hand-edited config looks like.
func Resolve(pref string) Lang {
	if l, ok := Normalize(pref); ok {
		return l
	}
	return Detect()
}
