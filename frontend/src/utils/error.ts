export function getErrorMessage(e: unknown, fallback: string): string {
  if (e && typeof e === 'object' && 'response' in e) {
    const resp = (e as { response?: { data?: { error?: string } } }).response
    if (resp?.data?.error) return resp.data.error
  }
  if (e instanceof Error) return e.message
  if (typeof e === 'string') return e
  return fallback
}