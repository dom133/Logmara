export const containerStateColor = (state: string | undefined): string => {
  if (state === 'running') return '#52c43a'
  if (state === 'paused') return '#faad14'
  if (state === 'restarting') return '#1890ff'
  if (state === 'unhealthy') return '#f5222d'
  if (state === 'dead') return '#d9d9d9'
  return '#d9d9d9'
}

export const containerStateIcon = (state: string | undefined): string => {
  if (state === 'running') return '✓'
  if (state === 'paused') return '⏸'
  if (state === 'restarting') return '↻'
  if (state === 'unhealthy') return '✗'
  if (state === 'dead') return '✗'
  return '?'
}
