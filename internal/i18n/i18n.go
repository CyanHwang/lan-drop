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
	EN Lang = "en"
	// A script subtag, not a region: the catalogue is about the writing system,
	// not about where the reader happens to live.
	ZHCN Lang = "zh-Hans"
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
	ErrMissingId         Msg = "err.missingId"
	ErrFileNotFound      Msg = "err.fileNotFound"
	ErrTargetIsFolder    Msg = "err.targetIsFolder"
	ErrInvalidName       Msg = "err.invalidName"
	ErrReadOnly          Msg = "err.readOnly"
	ErrDeleteRoot        Msg = "err.deleteRoot"
	ErrNotFound          Msg = "err.notFound"
	ErrOffsetMismatch    Msg = "err.offsetMismatch"
	ErrUploadBusy        Msg = "err.uploadBusy"
	ErrUploadRange       Msg = "err.uploadRange"
	ErrChunkCorrupt      Msg = "err.chunkCorrupt"
	ErrNoSpace           Msg = "err.noSpace"
	ErrFileChanged       Msg = "err.fileChanged"
	ErrClientGone        Msg = "err.clientGone"
	ErrUploadCancelled   Msg = "err.uploadCancelled"
	ErrServerStopped     Msg = "err.serverStopped"
	ErrFinalize          Msg = "err.finalize"
	ErrAppNotReady       Msg = "err.appNotReady"
	ErrClipboard         Msg = "err.clipboard"
	ErrServiceNotRunning Msg = "err.serviceNotRunning"
	ErrInvalidPath       Msg = "err.invalidPath"
	ErrConfigWrite       Msg = "err.configWrite"
	ErrNothingSelected   Msg = "err.nothingSelected"
	ErrTooManyItems      Msg = "err.tooManyItems"
)

// Desktop shell strings.
const (
	AppDescription   Msg = "app.description"
	AppRepo          Msg = "app.repo"
	AppQuit          Msg = "app.quit"
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
	CliOptThumbs     Msg = "cli.optThumbs"
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

// Browser UI strings. They are rendered into the page by the server; the
// handful that describe a running transfer are also shipped to the page as
// JSON, because those are produced after it has loaded.
const (
	WebHome          Msg = "web.home"
	WebPath          Msg = "web.path"
	WebBack          Msg = "web.back"
	WebBackAria      Msg = "web.backAria"
	WebLangButton    Msg = "web.langButton"
	WebSwitchToEN    Msg = "web.switchToEn"
	WebSwitchToZH    Msg = "web.switchToZh"
	WebUpload        Msg = "web.upload"
	WebUploadFiles   Msg = "web.uploadFiles"
	WebUploadFolder  Msg = "web.uploadFolder"
	WebNewFolder     Msg = "web.newFolder"
	WebHide          Msg = "web.hide"
	WebCancelAll     Msg = "web.cancelAll"
	WebDownloadZip   Msg = "web.downloadZip"
	WebSelectAll     Msg = "web.selectAll"
	WebClearAll      Msg = "web.clearAll"
	WebClearPick     Msg = "web.clearPick"
	WebSelectedCount Msg = "web.selectedCount"
	WebSelectItem    Msg = "web.selectItem"
	WebDropHere      Msg = "web.dropHere"
	WebNoShares      Msg = "web.noShares"
	WebSharedFolder  Msg = "web.sharedFolder"
	WebEmptyFolder   Msg = "web.emptyFolder"
	WebFolder        Msg = "web.folder"
	WebDownload      Msg = "web.download"
	WebDelete        Msg = "web.delete"
	WebMore          Msg = "web.more"
	WebView          Msg = "web.view"
	WebReadOnly      Msg = "web.readOnly"
	WebNotConfigured Msg = "web.notConfigured"
	WebShareCount    Msg = "web.shareCount"
	WebTruncated     Msg = "web.truncated"
	WebShowAll       Msg = "web.showAll"
	WebErrorTitle    Msg = "web.errorTitle"
	WebGoHome        Msg = "web.goHome"

	WebUploading       Msg = "web.uploading"
	WebUploadCount     Msg = "web.uploadCount"
	WebUploadDone      Msg = "web.uploadDone"
	WebActiveUploads   Msg = "web.activeUploads"
	WebDone            Msg = "web.done"
	WebFailed          Msg = "web.failed"
	WebWaiting         Msg = "web.waiting"
	WebServerDown      Msg = "web.serverDown"
	WebRetrying        Msg = "web.retrying"
	WebBusy            Msg = "web.busy"
	WebSizeMismatch    Msg = "web.sizeMismatch"
	WebResuming        Msg = "web.resuming"
	WebFilesFailed     Msg = "web.filesFailed"
	WebFilesUploaded   Msg = "web.filesUploaded"
	WebCancelled       Msg = "web.cancelled"
	WebStalled         Msg = "web.stalled"
	WebReadFailed      Msg = "web.readFailed"
	WebConnLost        Msg = "web.connLost"
	WebCancelAllAsk    Msg = "web.cancelAllAsk"
	WebUploadCancelled Msg = "web.uploadCancelled"
	WebConfirmDelete   Msg = "web.confirmDelete"
	WebDeleted         Msg = "web.deleted"
	WebDeleteFailed    Msg = "web.deleteFailed"
	WebDownloadStarted Msg = "web.downloadStarted"
	WebEnterShare      Msg = "web.enterShare"
	WebNewFolderName   Msg = "web.newFolderName"
	WebCreated         Msg = "web.created"
	WebCreateFailed    Msg = "web.createFailed"
	WebZipStarted      Msg = "web.zipStarted"
	WebSelectNothing   Msg = "web.selectNothing"
	WebTooManyPicked   Msg = "web.tooManyPicked"
	WebUnknownError    Msg = "web.unknownError"
	WebClose           Msg = "web.close"
	WebZoomIn          Msg = "web.zoomIn"
	WebZoomOut         Msg = "web.zoomOut"
	WebZoomFit         Msg = "web.zoomFit"
	WebCreatedLabel    Msg = "web.createdLabel"
	WebModified        Msg = "web.modified"
	WebSize            Msg = "web.size"
	WebVideoTodo       Msg = "web.videoTodo"
	WebPreviewFailed   Msg = "web.previewFailed"
	WebPreviewLoading  Msg = "web.previewLoading"
	WebPreviewLibFail  Msg = "web.previewLibFail"
	WebRowsOmitted     Msg = "web.rowsOmitted"
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
		ErrMissingId:         "missing upload id",
		ErrFileNotFound:      "file does not exist",
		ErrTargetIsFolder:    "target is a folder",
		ErrInvalidName:       "invalid file name",
		ErrReadOnly:          "server is in read-only mode",
		ErrDeleteRoot:        "refusing to delete the share root",
		ErrNotFound:          "not found",
		ErrOffsetMismatch:    "offset mismatch",
		ErrUploadBusy:        "another device is already uploading this file",
		ErrUploadRange:       "request body runs past the declared file size",
		ErrChunkCorrupt:      "chunk failed its checksum, retrying from %s",
		ErrNoSpace:           "not enough disk space: need %s, %s available",
		ErrFileChanged:       "staged data belongs to a different file",
		ErrClientGone:        "client disconnected after %s",
		ErrUploadCancelled:   "cancelled, %s discarded",
		ErrServerStopped:     "service stopped, %s discarded",
		ErrFinalize:          "cannot publish the finished file: %v",
		ErrAppNotReady:       "application is not ready",
		ErrClipboard:         "failed to write to the clipboard",
		ErrServiceNotRunning: "service is not running",
		ErrInvalidPath:       "invalid path: %v",
		ErrConfigWrite:       "cannot save configuration: %v",
		ErrNothingSelected:   "nothing selected",
		ErrTooManyItems:      "too many items selected (limit %d)",

		AppDescription:   "LAN file transfer",
		AppRepo:          "Open the GitHub repository",
		AppQuit:          "Quit LanDrop",
		TrayTooltip:      "LanDrop - LAN file transfer",
		TrayOpen:         "Open LanDrop",
		TrayQuit:         "Quit LanDrop",
		DialogPickFolder: "Choose a folder to share",

		CliUsageTitle:    "LanDrop server mode - headless HTTP file service",
		CliUsageLine:     "Usage:\n  %s [options]",
		CliOptsTitle:     "Options:",
		CliOptAddr:       "listen address, e.g. :57577, 0.0.0.0:9000, 127.0.0.1:57577",
		CliOptShare:      "shared folder; repeat the flag or separate with commas (overrides the config file)",
		CliOptConfig:     "config file path (default: lan-drop.ini next to the executable)",
		CliOptReadOnly:   "read-only mode: reject uploads, mkdir and delete",
		CliOptAutoPort:   "scan forward when the port is taken (default: fail loudly)",
		CliOptThumbs:     "generate thumbnail previews for images and videos (needs ffmpeg for video)",
		CliExample:       "Examples:\n  %s -share /data/photos -share /data/docs\n  %s -addr 0.0.0.0:9000 -config /etc/lan-drop/lan-drop.ini -readonly",
		CliNoShares:      "error: no shared folders.\n  Pass -share /path/to/dir, or add one to lan-drop.ini and retry.",
		CliBadAddr:       "error: invalid listen address %q: %v",
		CliBannerTitle:   "LanDrop is running (%s)",
		CliModeReadOnly:  "read-only",
		CliModeReadWrite: "read-write",
		CliShares:        "Shared folders (%d):",
		CliURLs:          "Reachable at:",
		CliStopHint:      "Press Ctrl+C to stop.",
		CliStopping:      "Stopping...",
		CliStopError:     "error while stopping: %v",

		WebHome:          "Home",
		WebPath:          "Path",
		WebBack:          "Back",
		WebBackAria:      "Go back",
		WebLangButton:    "中",
		WebSwitchToEN:    "Switch to English",
		WebSwitchToZH:    "Switch to Chinese",
		WebUpload:        "Upload",
		WebUploadFiles:   "Upload files",
		WebUploadFolder:  "Upload folder",
		WebNewFolder:     "New folder",
		WebHide:          "Hide",
		WebCancelAll:     "Cancel all",
		WebDownloadZip:   "Download zip",
		WebSelectAll:     "Select all",
		WebClearAll:      "Clear",
		WebClearPick:     "Clear selection",
		WebSelectedCount: "%d selected",
		WebSelectItem:    "Select %s",
		WebDropHere:      "Drop to upload",
		WebNoShares:      "No shared folders yet",
		WebSharedFolder:  "Shared folder",
		WebEmptyFolder:   "This folder is empty",
		WebFolder:        "Folder",
		WebDownload:      "Download",
		WebDelete:        "Delete",
		WebMore:          "More actions",
		WebView:          "View",
		WebReadOnly:      "read-only",
		WebNotConfigured: "Not configured",
		WebShareCount:    "%d shared folder(s)",
		WebTruncated:     "Showing the first %d of %d items",
		WebShowAll:       "Show all",
		WebErrorTitle:    "This page cannot be opened",
		WebGoHome:        "Back to shared folders",

		WebUploading:       "Uploading %d/%d",
		WebUploadCount:     "Upload %d/%d",
		WebUploadDone:      "Upload complete",
		WebActiveUploads:   "%d/%d uploading",
		WebDone:            "Done · %s",
		WebFailed:          "Failed: %s",
		WebWaiting:         "Waiting · %s",
		WebServerDown:      "Service stopped",
		WebRetrying:        "Retry %d: %s",
		WebBusy:            "Another device is uploading this file",
		WebSizeMismatch:    "Server reported a different size",
		WebResuming:        "Resuming %s",
		WebFilesFailed:     "%d file(s) failed",
		WebFilesUploaded:   "Uploaded %d file(s)",
		WebCancelled:       "Cancelled",
		WebStalled:         "Transfer stalled",
		WebReadFailed:      "Cannot read the file",
		WebConnLost:        "Connection lost",
		WebCancelAllAsk:    "Cancel all uploads? Data already sent will be deleted from the server.",
		WebUploadCancelled: "All uploads cancelled",
		WebConfirmDelete:   "Delete “%s”? This cannot be undone.",
		WebDeleted:         "Deleted",
		WebDeleteFailed:    "Delete failed: %s",
		WebDownloadStarted: "Downloading %s",
		WebEnterShare:      "Open a shared folder first",
		WebNewFolderName:   "New folder name",
		WebCreated:         "Created",
		WebCreateFailed:    "Create failed: %s",
		WebZipStarted:      "Packaging %d item(s)…",
		WebSelectNothing:   "Nothing selected",
		WebTooManyPicked:   "At most %d items per download",
		WebUnknownError:    "unknown error",
		WebClose:           "Close",
		WebZoomIn:          "Zoom in",
		WebZoomOut:         "Zoom out",
		WebZoomFit:         "Fit to window",
		WebCreatedLabel:    "Created",
		WebModified:        "Modified",
		WebSize:            "Size",
		WebVideoTodo:       "Video playback is not available yet",
		WebPreviewFailed:   "This file cannot be previewed",
		WebPreviewLoading:  "Loading preview…",
		WebPreviewLibFail:  "Could not load the preview component",
		WebRowsOmitted:     "Showing the first %d rows",
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
		ErrMissingId:         "缺少 upload id",
		ErrFileNotFound:      "文件不存在",
		ErrTargetIsFolder:    "目标是文件夹",
		ErrInvalidName:       "文件名无效",
		ErrReadOnly:          "服务处于只读模式",
		ErrDeleteRoot:        "拒绝删除共享根目录",
		ErrNotFound:          "不存在",
		ErrOffsetMismatch:    "偏移量不匹配",
		ErrUploadBusy:        "另一台设备正在上传这个文件",
		ErrUploadRange:       "请求体超出了声明的文件大小",
		ErrChunkCorrupt:      "分片校验失败，从 %s 重传",
		ErrNoSpace:           "磁盘空间不足：需要 %s，可用 %s",
		ErrFileChanged:       "暂存数据属于另一个文件",
		ErrClientGone:        "客户端已断开（已写入 %s）",
		ErrUploadCancelled:   "已取消（已丢弃 %s）",
		ErrServerStopped:     "服务已停止（已丢弃 %s）",
		ErrFinalize:          "无法落盘：%v",
		ErrAppNotReady:       "应用未就绪",
		ErrClipboard:         "写入剪贴板失败",
		ErrServiceNotRunning: "服务未启动",
		ErrInvalidPath:       "路径无效: %v",
		ErrConfigWrite:       "无法保存配置: %v",
		ErrNothingSelected:   "没有选择任何项目",
		ErrTooManyItems:      "选择项过多（上限 %d）",

		AppDescription:   "局域网快传",
		AppRepo:          "打开 GitHub 仓库",
		AppQuit:          "退出 LanDrop",
		TrayTooltip:      "LanDrop - 局域网快传",
		TrayOpen:         "打开 LanDrop",
		TrayQuit:         "退出 LanDrop",
		DialogPickFolder: "选择要共享的文件夹",

		CliUsageTitle:    "LanDrop 服务端模式 — 无界面，只提供局域网 HTTP 文件服务",
		CliUsageLine:     "用法：\n  %s [选项]",
		CliOptsTitle:     "选项：",
		CliOptAddr:       "监听地址，例如 :57577、0.0.0.0:9000、127.0.0.1:57577",
		CliOptShare:      "共享目录，可重复指定或用逗号分隔；指定后忽略配置文件",
		CliOptConfig:     "配置文件路径，默认为可执行文件同目录的 lan-drop.ini",
		CliOptReadOnly:   "只读模式：禁止上传、新建文件夹和删除",
		CliOptAutoPort:   "端口被占用时自动向后寻找可用端口（默认直接报错）",
		CliOptThumbs:     "生成图片和视频的缩略图预览（视频需要 ffmpeg）",
		CliExample:       "示例：\n  %s -share /data/photos -share /data/docs\n  %s -addr 0.0.0.0:9000 -config /etc/lan-drop/lan-drop.ini -readonly",
		CliNoShares:      "错误：没有可用的共享目录。\n  用 -share /path/to/dir 指定，或在 lan-drop.ini 中配置后重试。",
		CliBadAddr:       "错误：无效的监听地址 %q：%v",
		CliBannerTitle:   "LanDrop 服务已启动（%s模式）",
		CliModeReadOnly:  "只读",
		CliModeReadWrite: "读写",
		CliShares:        "共享目录 %d 个：",
		CliURLs:          "访问地址：",
		CliStopHint:      "按 Ctrl+C 停止。",
		CliStopping:      "正在停止…",
		CliStopError:     "停止时出错：%v",

		WebHome:          "根目录",
		WebPath:          "路径",
		WebBack:          "返回",
		WebBackAria:      "返回上一层",
		WebLangButton:    "EN",
		WebSwitchToEN:    "切换到英文",
		WebSwitchToZH:    "切换到中文",
		WebUpload:        "上传",
		WebUploadFiles:   "上传文件",
		WebUploadFolder:  "上传文件夹",
		WebNewFolder:     "新建文件夹",
		WebHide:          "收起",
		WebCancelAll:     "全部取消",
		WebDownloadZip:   "打包下载",
		WebSelectAll:     "全选",
		WebClearAll:      "取消全选",
		WebClearPick:     "取消选择",
		WebSelectedCount: "已选 %d 项",
		WebSelectItem:    "选择 %s",
		WebDropHere:      "松开即可上传",
		WebNoShares:      "还没有共享目录",
		WebSharedFolder:  "共享目录",
		WebEmptyFolder:   "这个文件夹是空的",
		WebFolder:        "文件夹",
		WebDownload:      "下载",
		WebDelete:        "删除",
		WebMore:          "更多操作",
		WebView:          "查看",
		WebReadOnly:      "只读",
		WebNotConfigured: "未配置",
		WebShareCount:    "%d 个共享目录",
		WebTruncated:     "仅显示前 %d 项，共 %d 项",
		WebShowAll:       "显示全部",
		WebErrorTitle:    "这个页面打不开",
		WebGoHome:        "回到共享目录",

		WebUploading:       "正在上传 %d/%d",
		WebUploadCount:     "上传 %d/%d",
		WebUploadDone:      "上传完成",
		WebActiveUploads:   "%d/%d 正在上传",
		WebDone:            "完成 · %s",
		WebFailed:          "失败：%s",
		WebWaiting:         "等待中 · %s",
		WebServerDown:      "服务已停止",
		WebRetrying:        "第 %d 次重试：%s",
		WebBusy:            "另一台设备正在上传这个文件",
		WebSizeMismatch:    "服务端返回的大小不一致",
		WebResuming:        "继续传输 %s",
		WebFilesFailed:     "%d 个文件失败",
		WebFilesUploaded:   "已上传 %d 个文件",
		WebCancelled:       "已取消",
		WebStalled:         "传输卡住",
		WebReadFailed:      "读取文件失败",
		WebConnLost:        "连接中断",
		WebCancelAllAsk:    "取消全部上传？已传输的临时数据会从服务端删除。",
		WebUploadCancelled: "已取消全部上传",
		WebConfirmDelete:   "删除“%s”？此操作不可恢复。",
		WebDeleted:         "已删除",
		WebDeleteFailed:    "删除失败：%s",
		WebDownloadStarted: "开始下载 %s",
		WebEnterShare:      "先进入一个共享目录",
		WebNewFolderName:   "新建文件夹名称",
		WebCreated:         "已创建",
		WebCreateFailed:    "创建失败：%s",
		WebZipStarted:      "正在打包 %d 项…",
		WebSelectNothing:   "还没有选择任何项目",
		WebTooManyPicked:   "一次最多打包 %d 项",
		WebUnknownError:    "未知错误",
		WebClose:           "关闭",
		WebZoomIn:          "放大",
		WebZoomOut:         "缩小",
		WebZoomFit:         "适应窗口",
		WebCreatedLabel:    "创建时间",
		WebModified:        "修改时间",
		WebSize:            "大小",
		WebVideoTodo:       "视频播放功能尚未启用",
		WebPreviewFailed:   "这个文件无法预览",
		WebPreviewLoading:  "正在加载预览…",
		WebPreviewLibFail:  "预览组件加载失败",
		WebRowsOmitted:     "仅显示前 %d 行",
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
		// Every Chinese tag maps to the single Chinese catalogue: a reader of
		// the other script is far better served by it than by English.
		return ZHCN, true
	case strings.HasPrefix(t, "en"):
		return EN, true
	}
	return "", false
}

// Resolve turns a stored preference into a concrete language. Only the
// canonical tags are honoured — Normalize is deliberately more forgiving than
// this, because it has to read whatever locale string the OS hands over, while
// a stored preference should be exactly what this app writes. Anything else
// falls back to the host OS, which is what a first run looks like.
func Resolve(pref string) Lang {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case string(EN):
		return EN
	case strings.ToLower(string(ZHCN)):
		return ZHCN
	}
	return Detect()
}
