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
