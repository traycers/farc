import type { ChannelInfo } from '../api/farcd'

type ChannelChecklistProps = {
  channels: ChannelInfo[]
  checked: Set<number>
  onToggle: (channel: number) => void
}

// ChannelChecklist is the player page's left-column channel picker
// (.scratch/player-redesign/spec.md) -- purely controlled, ticking a
// checkbox here loads nothing by itself, PlayerPage's "Поиск" button does.
export default function ChannelChecklist({ channels, checked, onToggle }: ChannelChecklistProps) {
  return (
    <ul className="list-unstyled player-channel-checklist">
      {channels.map((c) => (
        <li key={c.channel} className="form-check">
          <input
            type="checkbox"
            className="form-check-input"
            id={`player-channel-${c.channel}`}
            aria-label={`channel ${c.channel}`}
            checked={checked.has(c.channel)}
            onChange={() => onToggle(c.channel)}
          />
          <label className="form-check-label" htmlFor={`player-channel-${c.channel}`}>
            {c.name ?? `channel ${c.channel}`}
          </label>
        </li>
      ))}
    </ul>
  )
}
