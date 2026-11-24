// UI strings for the desktop window. The Go backend owns the same catalogue in
// internal/i18n; the two must stay in sync, because error messages arrive from
// Go already localised while everything else is rendered here.

export type Lang = 'en' | 'zh-CN'

export const LANGS: Lang[] = ['en', 'zh-CN']

type Dict = Record<string, string>

const en: Dict = {
  'app.tagline': 'LAN file transfer',
  'app.repo': 'Open the GitHub repository',
  'app.quit': 'Quit LanDrop',

  'status.running': 'Running · {port}',
  'status.stopped': 'Stopped',

  'shares.title': 'Shared folders',
  'shares.empty': 'No shared folders yet — click to add one',
  'shares.add': 'Add folder',
  'shares.reveal': 'Show in file manager',
  'shares.remove': 'Remove',
  'shares.removeLocked': 'Stop the service before removing',

  'service.start': 'Start service',
  'service.starting': 'Starting',
  'service.stop': 'Stop service',
  'service.openBrowser': 'Open in browser',
  'service.needShare': 'Add at least one folder to share first',

  'options.showHidden': 'Show hidden files',
  'options.showDotfiles': 'Show dot-prefixed files',
  'options.appliesTo': 'Applies to both files and folders',
  'options.filter': 'Filter out',
  'options.filterHint':
    'Comma or semicolon separated. Each entry is a name or a glob like *.tmp. Start an entry with re: to use a regular expression.',

  'steps.1': 'Add the folders you want to share',
  'steps.2': 'Start the service, then scan or type the address',
  'steps.3': 'Open it in any browser on the same network',

  'note.firewall':
    'The system may ask for firewall or network permission on first launch. You must allow it, or other devices cannot connect.',

  'connect.title': 'How to connect',
  'connect.qrPlaceholder': 'The QR code appears\nonce the service starts',
  'connect.copy': 'Copy',
  'connect.copied': 'Copied',
  'connect.noAddress': 'No LAN address detected. Check your network connection.',
  'connect.hint':
    'Connect a phone, tablet or another computer to the same network, then scan the code or type the address.',

  'activity.title': 'Transfers',
  'activity.active': '{count} in progress',
  'activity.clear': 'Clear',
  'activity.emptyTitle': 'No transfers yet',
  'activity.empty': 'When a device downloads or uploads a file, it shows up here in real time.',
  'activity.failed': 'Failed',
  'activity.download': 'Download',
  'activity.upload': 'Upload',
  'activity.clientIp': 'Client address',
  'activity.foot': 'Records are kept for this session only',

  'unit.min': '{m}m {s}s',

  'lang.label': 'Language',
  'lang.switchTo': 'Switch to Chinese',
}

const zh: Dict = {
  'app.tagline': '局域网快传',
  'app.repo': '打开 GitHub 仓库',
  'app.quit': '退出 LanDrop',

  'status.running': '运行中 · {port}',
  'status.stopped': '未启动',

  'shares.title': '共享目录',
  'shares.empty': '还没有共享目录，点击添加',
  'shares.add': '添加文件夹',
  'shares.reveal': '在文件管理器中打开',
  'shares.remove': '移除',
  'shares.removeLocked': '停止服务后才能移除',

  'service.start': '启动服务',
  'service.starting': '启动中',
  'service.stop': '停止服务',
  'service.openBrowser': '浏览器打开',
  'service.needShare': '先添加至少一个要共享的文件夹',

  'options.showHidden': '显示隐藏文件',
  'options.showDotfiles': '显示以 . 开头的文件',
  'options.appliesTo': '对文件和文件夹同时生效',
  'options.filter': '过滤掉',
  'options.filterHint':
    '用逗号或分号分隔。每项是名称或 *.tmp 这样的通配符；re: 开头表示正则表达式。',

  'steps.1': '添加要共享的文件夹',
  'steps.2': '启动服务，扫码或输入地址',
  'steps.3': '任意设备的浏览器打开即可互传',

  'note.firewall': '首次启动系统可能弹出防火墙或网络权限提示，必须允许，否则其它设备连不上。',

  'connect.title': '连接方式',
  'connect.qrPlaceholder': '启动服务后\n显示二维码',
  'connect.copy': '复制',
  'connect.copied': '已复制',
  'connect.noAddress': '未检测到局域网地址，请检查网络连接。',
  'connect.hint': '手机、平板或电脑连同一个网络，扫码或输入地址即可访问。',

  'activity.title': '传输活动',
  'activity.active': '{count} 进行中',
  'activity.clear': '清空',
  'activity.emptyTitle': '还没有传输记录',
  'activity.empty': '其它设备下载或上传文件时，会实时显示在这里。',
  'activity.failed': '失败',
  'activity.download': '下载',
  'activity.upload': '上传',
  'activity.clientIp': '客户端地址',
  'activity.foot': '记录仅保留在本次会话',

  'unit.min': '{m} 分 {s} 秒',

  'lang.label': '语言',
  'lang.switchTo': '切换到英文',
}

const dicts: Record<Lang, Dict> = { en, 'zh-CN': zh }

export function translate(
  lang: Lang,
  key: string,
  vars?: Record<string, string | number>,
): string {
  const table = dicts[lang] ?? en
  let text = table[key] ?? en[key] ?? key
  if (vars) {
    for (const [k, v] of Object.entries(vars)) {
      text = text.split(`{${k}}`).join(String(v))
    }
  }
  return text
}

export type TFunc = (key: string, vars?: Record<string, string | number>) => string

export function makeT(lang: Lang): TFunc {
  return (key, vars) => translate(lang, key, vars)
}

/** Maps an OS locale ("zh-Hans-CN") onto a supported UI language. */
export function normalizeLang(tag: string | undefined | null): Lang {
  const t = (tag ?? '').toLowerCase()
  return t.startsWith('zh') ? 'zh-CN' : 'en'
}
