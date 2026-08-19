// Decodes the raw decimal TreeNode.value of a small set of enum-like roles
// into human-readable text, per .scratch/fblocks-ui/issues/12-tree-codec-
// frame-kind-and-precise-time.md. Scoped to exactly codec(video)/
// codec(audio)/frame_kind -- backend keeps sending the raw byte (mirrors
// internal/api/fblocktree.go's generic formatNodeValue), this is a
// display-only decode.

// mediatree.CodecUninitialized is 0, shared by both enums below -- the Go
// zero value of an unset codec field, never a real codec (mediatree/role.go).
const UNINITIALIZED = 'uninitialized'
const VIDEO_CODECS: Record<string, string> = { '0': UNINITIALIZED, '1': 'H264', '2': 'H265' }
const AUDIO_CODECS: Record<string, string> = { '0': UNINITIALIZED, '1': 'PCM', '2': 'AAC', '3': 'PCMA', '4': 'PCMU' }
// mediatree.FrameKindI/FrameKindP are the ASCII codes '73'/'80', not '0'/'1'.
const FRAME_KINDS: Record<string, string> = { '73': 'I', '80': 'P' }

export function formatDecodedValue(role: string, value: string): string | undefined {
  if (role === 'codec(video)') return VIDEO_CODECS[value] ?? `unknown(${value})`
  if (role === 'codec(audio)') return AUDIO_CODECS[value] ?? `unknown(${value})`
  if (role === 'frame_kind') return FRAME_KINDS[value] ?? `unknown(${value})`
  return undefined
}
