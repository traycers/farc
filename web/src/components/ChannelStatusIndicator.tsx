// Two-circle channel status (.scratch/live-page/spec.md): an outer ring
// for farcd's own RTSP connection state (blue = connected, red =
// last_connect_error is set), and an inner dot for whether CapturePolicy
// is currently recording -- identical semantics/classes to what
// ChannelsIndexPage rendered inline before this component existed
// (issues/05-shared-status-indicator.md). Shared by ChannelsIndexPage and
// the Live page so both use the same visual language for channel status.
type ChannelStatusIndicatorProps = {
  channel: number
  connected: boolean
  recording: boolean
}

export default function ChannelStatusIndicator({ channel, connected, recording }: ChannelStatusIndicatorProps) {
  return (
    <span
      className={`channel-status-indicator ${connected ? 'channel-status-connected' : 'channel-status-disconnected'}`}
      data-testid={`channel-status-outer-${channel}`}
      title={connected ? 'connected' : 'disconnected'}
    >
      <span
        data-testid={`channel-recording-dot-${channel}`}
        className={`status-dot ${recording ? 'status-dot-recording' : 'status-dot-idle'}`}
        title={recording ? 'recording' : 'not recording'}
      />
    </span>
  )
}
