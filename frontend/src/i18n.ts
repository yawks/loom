import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import enTranslation from "./locales/en/translation.json";
import frTranslation from "./locales/fr/translation.json";

// Track missing keys to avoid duplicate logs
const missingKeys = new Set<string>();

i18n
  // detect user language
  .use(LanguageDetector)
  // pass the i18n instance to react-i18next.
  .use(initReactI18next)
  // init i18next
  .init({
    debug: false, // Disable default debug to use custom logging
    fallbackLng: "en",
    interpolation: {
      escapeValue: false, // not needed for react as it escapes by default
    },
    resources: {
      en: {
        translation: enTranslation,
      },
      fr: {
        translation: frTranslation,
      },
    },
    // Log missing translations
    missingKeyHandler: (lng, ns, key, fallbackValue) => {
      const languages = Array.isArray(lng) ? lng : [lng];
      const safeFallback = fallbackValue ?? key;
      const keyPath = `${languages.join(",")}:${ns}:${key}`;
      if (!missingKeys.has(keyPath)) {
        missingKeys.add(keyPath);
        console.warn(
          `[i18n] Missing translation key: "${key}" in language "${languages.join(",")}" (namespace: ${ns})`,
          `\n  Fallback value: "${safeFallback}"`,
          `\n  Add this to your translation file: "${key}": "${safeFallback}"`
        );
      }
    },
  });

export default i18n;
