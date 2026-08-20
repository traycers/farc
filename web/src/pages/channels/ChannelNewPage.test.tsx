import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import ChannelNewPage from './ChannelNewPage'

vi.mock('../../api/farcd', () => ({
  listStorages: vi.fn(async () => []),
  listChannels: vi.fn(async () => []),
  createChannel: vi.fn(async () => {}),
}))

function renderPage() {
  return render(
    <MemoryRouter>
      <ChannelNewPage />
    </MemoryRouter>,
  )
}

describe('ChannelNewPage rtsp_url generate button', () => {
  it('fills rtsp_url with the local test mediamtx stream on click', async () => {
    renderPage()

    fireEvent.click(await screen.findByTestId('rtsp-url-generate-btn'))

    expect(screen.getByPlaceholderText('rtsp://camera1/stream')).toHaveValue('rtsp://mediamtx:8554/test')
  })
})
