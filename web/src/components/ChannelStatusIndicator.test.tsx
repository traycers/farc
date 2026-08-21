import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import ChannelStatusIndicator from './ChannelStatusIndicator'

describe('ChannelStatusIndicator', () => {
  it('shows a blue outer ring when connected and a red inner dot when recording', () => {
    render(<ChannelStatusIndicator channel={1} connected recording />)

    expect(screen.getByTestId('channel-status-outer-1').className).toContain('channel-status-connected')
    expect(screen.getByTestId('channel-recording-dot-1').className).toContain('status-dot-recording')
  })

  it('shows a red outer ring when disconnected and a gray inner dot when idle', () => {
    render(<ChannelStatusIndicator channel={2} connected={false} recording={false} />)

    expect(screen.getByTestId('channel-status-outer-2').className).toContain('channel-status-disconnected')
    expect(screen.getByTestId('channel-recording-dot-2').className).toContain('status-dot-idle')
  })
})
