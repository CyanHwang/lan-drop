import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import QRCodeStyling from 'qr-code-styling'
import { AppService } from '../bindings/github.com/CyanHwang/lan-drop'
import { makeT, normalizeLang, type Lang, type TFunc } from './i18n'

type Share = {
  id: string
  name: string
  path: string
}

type Info = {
  running: boolean
  port: number
  urls: string[]
  since: number
  readOnly: boolean
  shares: Share[]
}

type ViewOptions = {
  showHidden: boolean
  showDotfiles: boolean
  filter: string
}

type Progress = {
  id: string
  name: string
  dir: string
  share: string
  direction: string
  loaded: number
  total: number
  done: boolean
  error?: string
  speed: number
  elapsedMs: number
  at: number
}

const EMPTY_INFO: Info = { running: false, port: 0, urls: [], since: 0, readOnly: false, shares: [] }
const EMPTY_OPTIONS: ViewOptions = { showHidden: false, showDotfiles: false, filter: '' }

function normalizeOptions(raw: unknown): ViewOptions {
  const v = (raw ?? {}) as Partial<ViewOptions>
  return {
    showHidden: Boolean(v.showHidden),
    showDotfiles: Boolean(v.showDotfiles),
    filter: typeof v.filter === 'string' ? v.filter : '',
  }
}

function normalizeShare(raw: unknown): Share | null {
  const v = (raw ?? {}) as Partial<Share>
  if (typeof v.id !== 'string' || typeof v.path !== 'string') return null
  return { id: v.id, name: typeof v.name === 'string' ? v.name : v.path, path: v.path }
}

function normalizeShares(raw: unknown): Share[] {
  if (!Array.isArray(raw)) return []
  const out: Share[] = []
  for (const item of raw) {
    const s = normalizeShare(item)
    if (s) out.push(s)
  }
  return out
}

// Go marshals nil slices as JSON null, so every field is treated as untrusted
// until it has been through here.
function normalizeInfo(raw: unknown): Info {
  const v = (raw ?? {}) as Partial<Info>
  return {
    running: Boolean(v.running),
    port: typeof v.port === 'number' ? v.port : 0,
    urls: Array.isArray(v.urls) ? v.urls.filter((u): u is string => typeof u === 'string') : [],
    since: typeof v.since === 'number' ? v.since : 0,
    readOnly: Boolean(v.readOnly),
    shares: normalizeShares(v.shares),
  }
}

function fmtSize(n: number): string {
  if (!n || n < 0) return '0 B'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let i = -1
  let v = n
  do {
    v /= 1024
    i++
  } while (v >= 1024 && i < units.length - 1)
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

function fmtSpeed(bps: number): string {
  if (!bps || bps <= 0) return ''
  return `${fmtSize(bps)}/s`
}

function fmtElapsed(ms: number, t: TFunc): string {
  if (ms < 1000) return `${ms} ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`
  const m = Math.floor(ms / 60000)
  const s = Math.round((ms % 60000) / 1000)
  return t('unit.min', { m, s })
}

function fmtClock(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  const now = new Date()
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  if (d.toDateString() === now.toDateString()) return hm
  return `${d.getMonth() + 1}/${d.getDate()} ${hm}`
}

const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.userAgent)

const ICO_FOLDER = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinejoin="round">
    <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
  </svg>
)

const ICO_PLUS = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
    <path d="M12 5v14M5 12h14" />
  </svg>
)

const ICO_CLOSE = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
    <path d="M6 6l12 12M18 6L6 18" />
  </svg>
)

const ICO_UP = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 19V5M6 11l6-6 6 6" />
  </svg>
)

const ICO_DOWN = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 5v14M6 13l6 6 6-6" />
  </svg>
)

const ICO_EXTERNAL = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
    <path d="M14 4h6v6M20 4l-9 9M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4" />
  </svg>
)

const ICO_REVEAL = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
    <path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3" />
    <rect x="8" y="8" width="8" height="8" rx="1.5" />
  </svg>
)

const ICO_GITHUB = (
  <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M12 .5C5.73.5.5 5.73.5 12c0 5.08 3.29 9.39 7.86 10.91.58.11.79-.25.79-.56v-2c-3.2.7-3.88-1.54-3.88-1.54-.53-1.34-1.29-1.7-1.29-1.7-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.55-.29-5.23-1.28-5.23-5.7 0-1.26.45-2.29 1.19-3.1-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.18 1.18a11 11 0 0 1 5.8 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.19 1.84 1.19 3.1 0 4.43-2.69 5.41-5.25 5.69.41.36.78 1.06.78 2.14v3.17c0 .31.21.68.8.56A11.5 11.5 0 0 0 23.5 12C23.5 5.73 18.27.5 12 .5z" />
  </svg>
)

const ICO_POWER = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 3v9" />
    <path d="M6.4 6.4a8 8 0 1 0 11.2 0" />
  </svg>
)

const ICO_LANG = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
    <circle cx="12" cy="12" r="9" />
    <path d="M3 12h18M12 3c2.4 2.6 3.7 5.6 3.7 9s-1.3 6.4-3.7 9c-2.4-2.6-3.7-5.6-3.7-9S9.6 5.6 12 3z" />
  </svg>
)

const ICO_IDLE = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 15V3M7 8l5-5 5 5" />
    <path d="M4 14v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4" />
  </svg>
)

export default function App() {
  const [info, setInfo] = useState<Info>(EMPTY_INFO)
  const [shares, setShares] = useState<Share[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [items, setItems] = useState<Progress[]>([])
  const [copied, setCopied] = useState('')
  const [opts, setOpts] = useState<ViewOptions>(EMPTY_OPTIONS)
  // Kept separate from opts so typing does not hit the backend on every key.
  const [filterDraft, setFilterDraft] = useState('')
  const [lang, setLang] = useState<Lang>('en')

  const t = useMemo(() => makeT(lang), [lang])

  const copyTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const qrHostRef = useRef<HTMLDivElement | null>(null)
  const qrRef = useRef<QRCodeStyling | null>(null)

  const urls = Array.isArray(info.urls) ? info.urls : []
  const primaryUrl = urls.length > 0 ? urls[0] : ''
  const locked = info.running

  useEffect(() => {
    // The backend already resolved and persisted the language, so this is a
    // single read: no detection, no flash of the wrong locale.
    AppService.Language()
      .then((tag) => setLang(normalizeLang(tag)))
      .catch(() => undefined)

    AppService.Shares()
      .then((raw: unknown) => setShares(normalizeShares(raw)))
      .catch(() => undefined)

    AppService.ViewOptions()
      .then((raw: unknown) => {
        const next = normalizeOptions(raw)
        setOpts(next)
        setFilterDraft(next.filter)
      })
      .catch(() => undefined)

    AppService.Status()
      .then((raw: unknown) => {
        const next = normalizeInfo(raw)
        setInfo(next)
        if (next.running && next.shares.length > 0) setShares(next.shares)
      })
      .catch(() => undefined)

    const off = Events.On('transfer:progress', (ev: { data: Progress }) => {
      const p = ev.data
      if (!p || !p.id) return
      setItems((prev) => {
        const idx = prev.findIndex((x) => x.id === p.id)
        if (idx === -1) return [p, ...prev].slice(0, 80)
        const next = prev.slice()
        next[idx] = p
        return next
      })
    })

    return () => {
      if (typeof off === 'function') off()
    }
  }, [])

  // Render the QR code with qr-code-styling. Kept white-on-dark deliberately:
  // inverted QR codes fail on a meaningful share of scanner apps.
  useEffect(() => {
    const host = qrHostRef.current
    if (!info.running || !primaryUrl || !host) {
      if (host) host.innerHTML = ''
      qrRef.current = null
      return
    }

    if (!qrRef.current) {
      qrRef.current = new QRCodeStyling({
        type: 'svg',
        width: 360,
        height: 360,
        data: primaryUrl,
        margin: 0,
        qrOptions: { errorCorrectionLevel: 'M' },
        dotsOptions: { type: 'extra-rounded', color: '#14171f' },
        cornersSquareOptions: { type: 'extra-rounded', color: '#3b6cf6' },
        cornersDotOptions: { type: 'dot', color: '#3b6cf6' },
        backgroundOptions: { color: '#ffffff' },
      })
      qrRef.current.append(host)
    } else {
      qrRef.current.update({ data: primaryUrl })
    }
  }, [info.running, primaryUrl])

  const addShare = useCallback(async () => {
    setError('')
    try {
      setShares(normalizeShares(await AppService.AddShare()))
    } catch (e) {
      setError(String((e as Error)?.message ?? e))
    }
  }, [])

  const removeShare = useCallback(async (id: string) => {
    setError('')
    try {
      setShares(normalizeShares(await AppService.RemoveShare(id)))
    } catch (e) {
      setError(String((e as Error)?.message ?? e))
    }
  }, [])

  const start = useCallback(async () => {
    if (shares.length === 0) {
      setError(t('service.needShare'))
      return
    }
    setBusy(true)
    setError('')
    try {
      // 0 lets the backend pick its default port; the number lives in
      // internal/transfer.DefaultPort, not here.
      const s = normalizeInfo(await AppService.Start(0))
      setInfo(s)
      if (s.shares.length > 0) setShares(s.shares)
    } catch (e) {
      setError(String((e as Error)?.message ?? e))
    } finally {
      setBusy(false)
    }
  }, [shares.length, t])

  const stop = useCallback(async () => {
    setBusy(true)
    setError('')
    try {
      setInfo(normalizeInfo(await AppService.Stop()))
    } catch (e) {
      setError(String((e as Error)?.message ?? e))
    } finally {
      setBusy(false)
    }
  }, [])

  const copy = useCallback(async (text: string) => {
    try {
      await AppService.CopyText(text)
    } catch {
      try {
        await navigator.clipboard.writeText(text)
      } catch {
        /* clipboard unavailable inside the webview */
      }
    }
    setCopied(text)
    clearTimeout(copyTimer.current)
    copyTimer.current = setTimeout(() => setCopied(''), 1600)
  }, [])

  const toggleOption = useCallback(
    async (key: keyof ViewOptions) => {
      const previous = opts
      const next = { ...opts, [key]: !opts[key] }
      // Optimistic: a checkbox that lags behind the click feels broken.
      setOpts(next)
      try {
        setOpts(normalizeOptions(await AppService.SetViewOptions(next)))
        setError('')
      } catch (e) {
        setOpts(previous)
        setError(String((e as Error)?.message ?? e))
      }
    },
    [opts],
  )

  // Saved on blur rather than on every keystroke: the value is a free-text
  // rule list and rewriting config.ini per character would be wasteful.
  const commitFilter = useCallback(async () => {
    if (filterDraft === opts.filter) return
    const previous = opts
    const next = { ...opts, filter: filterDraft }
    setOpts(next)
    try {
      setOpts(normalizeOptions(await AppService.SetViewOptions(next)))
      setError('')
    } catch (e) {
      setOpts(previous)
      setFilterDraft(previous.filter)
      setError(String((e as Error)?.message ?? e))
    }
  }, [filterDraft, opts])

  const changeLang = useCallback(async (next: Lang) => {
    try {
      const applied = await AppService.SetLanguage(next)
      setLang(normalizeLang(applied))
      setError('')
    } catch (e) {
      setError(String((e as Error)?.message ?? e))
    }
  }, [])

  const toggleLang = useCallback(() => {
    void changeLang(lang === 'en' ? 'zh-CN' : 'en')
  }, [lang, changeLang])

  const active = items.filter((x) => !x.done).length

  return (
    <div className="app">
      <header className={isMac ? 'topbar mac' : 'topbar'}>
        <div className="logo">
          Lan<em>Drop</em>
        </div>
        <span className={info.running ? 'pill live' : 'pill'}>
          <span className="dot" />
          {info.running ? t('status.running', { port: info.port }) : t('status.stopped')}
        </span>
        <span className="spacer" />
        <span className="hint">{t('app.tagline')}</span>
        <button
          className="repo-btn"
          type="button"
          onClick={() => AppService.OpenRepository().catch(() => undefined)}
          aria-label={t('app.repo')}
          title={t('app.repo')}
        >
          {ICO_GITHUB}
        </button>
        <button
          className="lang-toggle"
          type="button"
          onClick={toggleLang}
          aria-label={t('lang.label')}
          title={t('lang.switchTo')}
        >
          {ICO_LANG}
          {lang === 'en' ? '中文' : 'EN'}
        </button>
        <button
          className="quit-btn"
          type="button"
          onClick={() => AppService.Quit().catch(() => undefined)}
          aria-label={t('app.quit')}
          title={t('app.quit')}
        >
          {ICO_POWER}
        </button>
      </header>

      <div className="main">
        <section className="card pane-shares">
          <div className="card-label">
            {t('shares.title')}
            {shares.length > 0 && <span className="label-count">{shares.length}</span>}
          </div>

          <div className="share-list">
            {shares.length === 0 ? (
              <button className="share-empty" onClick={addShare} type="button">
                {ICO_PLUS}
                {t('shares.empty')}
              </button>
            ) : (
              shares.map((sh) => (
                <div className="share-item" key={sh.id}>
                  <span className="share-ico">{ICO_FOLDER}</span>
                  <span className="share-meta">
                    <span className="share-name">{sh.name}</span>
                    <span className="share-path" title={sh.path}>
                      {sh.path}
                    </span>
                  </span>
                  {locked && (
                    <button
                      className="icon-btn"
                      onClick={() => AppService.RevealShare(sh.id).catch(() => undefined)}
                      title={t('shares.reveal')}
                    >
                      {ICO_REVEAL}
                    </button>
                  )}
                  <button
                    className="icon-btn danger"
                    onClick={() => removeShare(sh.id)}
                    disabled={locked}
                    title={locked ? t('shares.removeLocked') : t('shares.remove')}
                  >
                    {ICO_CLOSE}
                  </button>
                </div>
              ))
            )}
          </div>

          {!locked && shares.length > 0 && (
            <button className="add-share" onClick={addShare} type="button">
              {ICO_PLUS}
              {t('shares.add')}
            </button>
          )}

          <div className="view-opts" title={t('options.appliesTo')}>
            <label className="check">
              <input type="checkbox" checked={opts.showHidden} onChange={() => toggleOption('showHidden')} />
              <span>{t('options.showHidden')}</span>
            </label>
            <label className="check">
              <input type="checkbox" checked={opts.showDotfiles} onChange={() => toggleOption('showDotfiles')} />
              <span>{t('options.showDotfiles')}</span>
            </label>
          </div>

          <label className="filter-field" title={t('options.filterHint')}>
            <span>{t('options.filter')}</span>
            <input
              className="filter-input"
              type="text"
              value={filterDraft}
              placeholder="*.tmp, node_modules, re:^\\.git$"
              spellCheck={false}
              autoComplete="off"
              onChange={(e) => setFilterDraft(e.target.value)}
              onBlur={commitFilter}
              onKeyDown={(e) => {
                if (e.key === 'Enter') e.currentTarget.blur()
                if (e.key === 'Escape') {
                  setFilterDraft(opts.filter)
                  e.currentTarget.blur()
                }
              }}
            />
          </label>

          <div className="btn-row">
            {info.running ? (
              <button className="btn danger" onClick={stop} disabled={busy}>
                {t('service.stop')}
              </button>
            ) : (
              <button className="btn primary" onClick={start} disabled={busy}>
                {busy && <span className="spinner" />}
                {busy ? t('service.starting') : t('service.start')}
              </button>
            )}
            {info.running && (
              <button className="btn ghost" onClick={() => AppService.OpenInBrowser().catch(() => undefined)}>
                {ICO_EXTERNAL}
                {t('service.openBrowser')}
              </button>
            )}
          </div>

          {error && <div className="err-msg">{error}</div>}

          <ol className="steps">
            <li>
              <span className="num">1</span>
              {t('steps.1')}
            </li>
            <li>
              <span className="num">2</span>
              {t('steps.2')}
            </li>
            <li>
              <span className="num">3</span>
              {t('steps.3')}
            </li>
          </ol>

          <div className="note">{t('note.firewall')}</div>
        </section>

        <section className="card pane-connect">
          <div className="card-label">{t('connect.title')}</div>

          <div className="qr-wrap">
            {info.running && primaryUrl ? (
              <div className="qr-frame" key={primaryUrl}>
                <div ref={qrHostRef} />
              </div>
            ) : (
              <div className="qr-frame blank">
                {t('connect.qrPlaceholder')
                  .split('\n')
                  .map((line, i) => (
                    <span key={i}>
                      {i > 0 && <br />}
                      {line}
                    </span>
                  ))}
              </div>
            )}
          </div>

          <div className="urls">
            {info.running && urls.length > 0 ? (
              urls.map((u) => (
                <div className="url-row" key={u}>
                  <span className="url-text" title={u}>
                    {u}
                  </span>
                  <button className={copied === u ? 'copy done' : 'copy'} onClick={() => copy(u)}>
                    {copied === u ? t('connect.copied') : t('connect.copy')}
                  </button>
                </div>
              ))
            ) : (
              <div className="card-hint">{info.running ? t('connect.noAddress') : t('connect.hint')}</div>
            )}
          </div>
        </section>

        <section className="activity pane-activity">
          <div className="activity-head">
            <h2>{t('activity.title')}</h2>
            {items.length > 0 && (
              <span className="count">{active > 0 ? t('activity.active', { count: active }) : items.length}</span>
            )}
            <span className="spacer" />
            {items.length > 0 && (
              <button className="link-btn" onClick={() => setItems([])}>
                {t('activity.clear')}
              </button>
            )}
          </div>
          <div className="activity-list">
            {items.length === 0 ? (
              <div className="activity-empty">
                {ICO_IDLE}
                {t('activity.empty')}
              </div>
            ) : (
              items.map((x) => {
                const pct = x.total > 0 ? Math.min(100, (x.loaded / x.total) * 100) : 0
                const cls = x.done ? (x.error ? 'act failed' : 'act done') : 'act running'
                const where = [x.share, x.dir].filter(Boolean).join(' / ')
                return (
                  <div className={x.direction === 'down' ? `${cls} down` : cls} key={x.id}>
                    <span className="dir-ico">{x.direction === 'down' ? ICO_DOWN : ICO_UP}</span>
                    <div className="act-body">
                      <div className="act-line1">
                        <span className="act-name" title={x.name}>
                          {x.name}
                        </span>
                        {where && <span className="act-dir">{where}</span>}
                        <span className="act-time">{fmtClock(x.at)}</span>
                      </div>
                      <div className="act-bar">
                        <i style={{ width: `${pct}%` }} />
                      </div>
                    </div>
                    <div className="act-stat">
                      {x.error ? (
                        <strong>{t('activity.failed')}</strong>
                      ) : x.done ? (
                        <strong>{fmtSize(x.total || x.loaded)}</strong>
                      ) : (
                        <strong>{Math.round(pct)}%</strong>
                      )}
                      {x.error ? x.error : x.done ? fmtElapsed(x.elapsedMs, t) : fmtSpeed(x.speed)}
                    </div>
                  </div>
                )
              })
            )}
          </div>
        </section>
      </div>
    </div>
  )
}
