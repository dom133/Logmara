import { t } from 'i18next'

interface ApiErrorData {
  error?: string
  error_key?: string
  details?: string
}

export function getErrorMessage(e: unknown, fallback: string): string {
  if (e && typeof e === 'object' && 'response' in e) {
    const resp = (e as { response?: { data?: ApiErrorData } }).response
    const data = resp?.data
    if (data?.error_key) {
      const translated = t(`error.${data.error_key}`, { defaultValue: data.error_key })
      return translated
    }
    if (data?.error) return data.error
  }
  if (e instanceof Error) return e.message
  if (typeof e === 'string') return e
  return fallback
}