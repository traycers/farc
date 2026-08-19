import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import ChannelChecklist from './ChannelChecklist'
import type { ChannelInfo } from '../api/farcd'

function channel(overrides: Partial<ChannelInfo>): ChannelInfo {
  return {
    channel: 1,
    rtsp_url: 'rtsp://example',
    storage: 's1',
    capture_policy_type: 'continuous',
    prerecord_ns: 0,
    postrecord_ns: 0,
    ...overrides,
  }
}

describe('ChannelChecklist', () => {
  it('renders one checkbox per channel, labeled by channel number, checked per the controlled set', () => {
    const channels = [channel({ channel: 1 }), channel({ channel: 2, name: 'Entrance' })]
    render(<ChannelChecklist channels={channels} checked={new Set([2])} onToggle={() => {}} />)

    const cb1 = screen.getByLabelText('channel 1') as HTMLInputElement
    const cb2 = screen.getByLabelText('channel 2') as HTMLInputElement
    expect(cb1.checked).toBe(false)
    expect(cb2.checked).toBe(true)
  })

  it('calls onToggle with the clicked channel number', () => {
    const onToggle = vi.fn()
    render(<ChannelChecklist channels={[channel({ channel: 5 })]} checked={new Set()} onToggle={onToggle} />)

    fireEvent.click(screen.getByLabelText('channel 5'))
    expect(onToggle).toHaveBeenCalledWith(5)
  })
})
