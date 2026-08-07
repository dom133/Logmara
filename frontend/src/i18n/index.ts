import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

const LANGUAGE_PATH = '/locales'

// Minimal English fallback so the login page can render immediately without
// waiting for the full async init (locale fetch + backend call). This is
// only used for the first frame; initI18n() replaces these resources once
// the real locale data arrives.
const FALLBACK_EN: Record<string, string> = {
  'login.subtitle': 'Syslog management platform',
  'login.username': 'Username',
  'login.password': 'Password',
  'login.remember': 'Remember this device',
  'login.login': 'Log in',
  'login.loggedIn': 'Logged in successfully',
  'login.invalidCredentials': 'Invalid credentials',
  'login.loginFailed': 'Login failed',
  'login.passwordExpired': 'Password expired',
  'login.passwordExpiredDescription': 'Your password has expired. Please change it now.',
  'login.currentPassword': 'Current password',
  'login.newPassword': 'New password',
  'login.confirmPassword': 'Confirm password',
  'login.passwordMismatch': 'Passwords do not match',
  'login.passwordChanged': 'Password changed successfully',
  'login.passwordChangeFailed': 'Password change failed',
}

export function initI18nFallback(): typeof i18n {
  if ((i18n as any)._fallbackInitialized) return i18n
  ;(i18n as any)._fallbackInitialized = true
  i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    supportedLngs: ['en'],
    resources: { en: { translation: FALLBACK_EN } },
    interpolation: { escapeValue: false },
  })
  return i18n
}

// Overrides for cases where the platform's own name for a language isn't
// what we want to show; otherwise names come from Intl.DisplayNames so a
// newly added locale needs no code change here.
const LANGUAGE_NAME_OVERRIDES: Record<string, string> = {}

export function languageDisplayName(code: string): string {
  const fallback = code.charAt(0).toUpperCase() + code.slice(1)
  if (LANGUAGE_NAME_OVERRIDES[code]) return LANGUAGE_NAME_OVERRIDES[code]
  try {
    const name = new Intl.DisplayNames([code], { type: 'language' }).of(code)
    if (!name) return fallback
    return name.charAt(0).toLocaleUpperCase(code) + name.slice(1)
  } catch {
    return fallback
  }
}

export function sortLanguagesEnglishFirst(languages: string[]): string[] {
  return [...languages].sort((a, b) => {
    if (a === b) return 0
    if (a === 'en') return -1
    if (b === 'en') return 1
    return languageDisplayName(a).localeCompare(languageDisplayName(b))
  })
}

async function detectLanguages(): Promise<string[]> {
  try {
    const indexRes = await fetch(`${LANGUAGE_PATH}/index.json`)
    if (!indexRes.ok) return ['en']
    const index: string[] = await indexRes.json()
    return index.length > 0 ? index : ['en']
  } catch {
    return ['en']
  }
}

async function loadLanguage(lang: string): Promise<{ lang: string; data: any } | null> {
  try {
    const res = await fetch(`${LANGUAGE_PATH}/${lang}/translation.json`)
    if (res.ok) {
      const data = await res.json()
      return { lang, data }
    }
  } catch { /* ignore */ }
  return null
}

async function getDefaultLanguage(): Promise<string | null> {
  try {
    const res = await fetch('/api/settings/default-language')
    if (!res.ok) return null
    const data: { default_language?: string } = await res.json()
    return data.default_language || null
  } catch {
    return null
  }
}

export async function initI18n(): Promise<typeof i18n> {
  const [languages, defaultLang] = await Promise.all([
    detectLanguages(),
    getDefaultLanguage(),
  ])

  const savedLang = localStorage.getItem('syslog_lang')
  const activeLang = (savedLang && languages.includes(savedLang))
    ? savedLang
    : (defaultLang && languages.includes(defaultLang))
      ? defaultLang
      : languages[0]

  // Only load the active language up-front so the login page can render
  // immediately. The remaining languages are loaded in the background
  // (non-blocking) so they're ready when the user switches language.
  const activeResource = await loadLanguage(activeLang)
  const resources: Record<string, any> = {}
  if (activeResource) {
    resources[activeLang] = { translation: activeResource.data }
  }

  // Background-load every other language so the switcher works instantly.
  // This is intentionally fire-and-forget — a failure here only means the
  // user will see a brief delay on their first language switch.
  const remaining = languages.filter(l => l !== activeLang)
  if (remaining.length) {
    Promise.allSettled(remaining.map(loadLanguage)).then((results) => {
      for (const r of results as PromiseSettledResult<{ lang: string; data: any } | null>[]) {
        if (r.status === 'fulfilled' && r.value) {
          i18n.addResourceBundle(r.value.lang, 'translation', r.value.data, true, true)
        }
      }
    })
  }

  await i18n
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      resources,
      lng: activeLang,
      fallbackLng: languages[0],
      supportedLngs: languages,
      detection: {
        order: [],
        caches: [],
      },
      interpolation: {
        escapeValue: false,
      },
    })

  return i18n
}

export default i18n
