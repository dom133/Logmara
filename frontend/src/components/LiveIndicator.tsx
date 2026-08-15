import { Tooltip } from 'antd'
import { useLive } from '../App'

export default function LiveIndicator() {
  const { liveActive } = useLive()

  if (!liveActive) return null

  return (
    <Tooltip title="Live updates">
      <span
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 6,
          fontSize: 11,
          fontWeight: 600,
          color: '#cf1322',
          textTransform: 'uppercase',
          letterSpacing: 0.5,
          userSelect: 'none',
        }}
      >
        <span
          style={{
            width: 8,
            height: 8,
            borderRadius: '50%',
            background: '#cf1322',
            display: 'inline-block',
            animation: 'pulse 1.5s ease-in-out infinite',
          }}
        />
        LIVE
      </span>
    </Tooltip>
  )
}
