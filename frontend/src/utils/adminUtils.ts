export const containerStateColor = (state: string | undefined): string => {
  if (state === 'running') return 'green'
  if (state === 'paused') return 'gold'
  if (state === 'restarting') return 'orange'
  if (state === 'exited' || state === 'dead') return 'red'
  return 'default'
}

export const containerStateIcon = (state: string | undefined): string => {
  if (state === 'running') return '✓'
  if (state === 'paused') return '⏸'
  if (state === 'restarting') return '↻'
  if (state === 'exited' || state === 'dead') return '✗'
  return '?'
}

export const serviceStateColor = (state: string): string => {
  if (state === 'running') return 'green'
  if (state === 'partial') return 'gold'
  if (state === 'degraded') return 'orange'
  return 'default'
}

export const serviceStateIcon = (state: string): string => {
  if (state === 'running') return '✓'
  if (state === 'partial') return '⚠'
  if (state === 'degraded') return '⚡'
  return '·'
}

export const serviceStateLabel = (state: string): string => {
  if (state === 'running') return 'Running'
  if (state === 'partial') return 'Partial'
  if (state === 'degraded') return 'Degraded'
  return 'None'
}
