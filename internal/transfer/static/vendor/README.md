# Vendored preview libraries

These files are loaded on demand by the in-page viewer, never on a normal
listing. They are checked in so a LAN transfer box works with no internet
access and the main binary stays a single file.

| File | Version | License | Upstream |
|---|---|---|---|
| `marked.min.js` | 12.0.2 | MIT | https://github.com/markedjs/marked |
| `purify.min.js` (DOMPurify) | 3.1.6 | MPL-2.0 OR GPL-2.0-or-later | https://github.com/cure53/DOMPurify |
| `mammoth.browser.min.js` | 1.8.0 | BSD-2-Clause | https://github.com/mwilliamson/mammoth.js |
| `xlsx.mini.min.js` (SheetJS CE) | 0.18.5 | Apache-2.0 | https://github.com/SheetJS/sheetjs |

DOMPurify is not optional: Markdown may contain raw HTML, and a file on the
LAN must not be able to run script in the page's origin. SheetJS is the `mini`
build — enough to render a spreadsheet, a fraction of the full bundle.

To update one, download the same file from the versioned jsDelivr URL in the
table's upstream project and keep this list in step.
