import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

const LANGUAGE_PATH = '/locales'

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
  const resources: Record<string, any> = {}
  for (const lang of languages) {
    try {
      const res = await fetch(`${LANGUAGE_PATH}/${lang}/translation.json`)
      if (res.ok) {
        resources[lang] = { translation: await res.json() }
      }
    } catch {
      // skip unavailable languages
    }
  }
  return resources
}

async function getDefaultLanguage(): Promise<string | null> {
  try {
    const res = await fetch('/api/settings')
    if (!res.ok) return null
    const settings: Record<string, string> = await res.json()
    return settings['default_language'] || null
  } catch {
    return null
  }
}

export async function initI18n(): Promise<typeof i18n> {
  const languages = await detectLanguages()
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

  const defaultLang = await getDefaultLanguage()
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
