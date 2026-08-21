import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import LiveVideoTile from './LiveVideoTile'
import { connectWhep } from '../api/whep'

vi.mock('../api/whep', () => ({
  connectWhep: vi.fn(),
}))

// jsdom has no RTCPeerConnection, so whepUrl !== null exercises this
// component's real "WebRTC not supported" branch here -- same "thin
// coverage in jsdom, real exercising happens elsewhere" story as
// VideoTile.test.tsx, not a test-only code path.
describe('LiveVideoTile', () => {
  it('shows a "no signal" placeholder when there is no WHEP URL yet', () => {
    render(<LiveVideoTile channel={1} whepUrl={null} muted active={false} onClick={() => {}} />)
    expect(screen.getByTestId('live-video-tile-placeholder')).toBeInTheDocument()
  })

  it('reflects muted/active props on the underlying video element and container class', () => {
    render(<LiveVideoTile channel={1} whepUrl={null} muted={false} active onClick={() => {}} />)
    const video = document.querySelector('video') as HTMLVideoElement
    expect(video.muted).toBe(false)
    expect(screen.getByTestId('live-video-tile').className).toContain('live-video-tile-active')
  })

  it('calls onClick when the tile is clicked', () => {
    const onClick = vi.fn()
    render(<LiveVideoTile channel={1} whepUrl={null} muted active={false} onClick={onClick} />)
    fireEvent.click(screen.getByTestId('live-video-tile'))
    expect(onClick).toHaveBeenCalled()
  })

  describe('when a WHEP connection fails (e.g. mediamtx has no source yet)', () => {
    afterEach(() => {
      vi.unstubAllGlobals()
    })

    it('shows the same "no signal" placeholder instead of the raw thrown error', async () => {
      vi.stubGlobal('RTCPeerConnection', class {})
      vi.mocked(connectWhep).mockRejectedValue(new Error('whep: POST /api/whep/3/whep: 400 Bad Request'))

      render(<LiveVideoTile channel={3} whepUrl="/api/whep/3/whep" muted active={false} onClick={() => {}} />)

      const placeholder = await screen.findByTestId('live-video-tile-placeholder')
      expect(placeholder.textContent).toContain('нет сигнала')
      expect(placeholder.textContent).not.toContain('Bad Request')
    })
  })
})
