import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

const LANGUAGE_PATH = '/locales'

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

async function loadLanguageResources(languages: string[]): Promise<Record<string, any>> {
  const results = await Promise.allSettled(
    languages.map(async (lang) => {
      const res = await fetch(`${LANGUAGE_PATH}/${lang}/translation.json`)
      if (res.ok) {
        const data = await res.json()
        return { lang, data }
      }
      return null
    }),
  )
  const resources: Record<string, any> = {}
  for (const r of results) {
    if (r.status === 'fulfilled' && r.value) {
      resources[r.value.lang] = { translation: r.value.data }
    }
  }
  return resources
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
  const resources = await loadLanguageResources(languages)

  const savedLang = localStorage.getItem('syslog_lang')
  if (savedLang && resources[savedLang]) {
    const detectedLang = savedLang
    await i18n
      .use(LanguageDetector)
      .use(initReactI18next)
      .init({
        resources,
        lng: detectedLang,
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

  const detectedLang = defaultLang && resources[defaultLang] ? defaultLang : languages[0]

  await i18n
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      resources,
      lng: detectedLang,
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
