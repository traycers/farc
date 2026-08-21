// downloadTextFile saves text content as a client-side file download --
// blob + a hidden <a download> click, no server round-trip. No such helper
// existed anywhere in web/src before (settled via grilling, 2026-08-21, for
// the fblock tree's txt/csv export buttons).
export function downloadTextFile(filename: string, content: string, mimeType = 'text/plain'): void {
  const blob = new Blob([content], { type: mimeType })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}
