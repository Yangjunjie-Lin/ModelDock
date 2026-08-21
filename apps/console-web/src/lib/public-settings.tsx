import { createContext, type ReactNode, useContext, useMemo, useState } from 'react'

type PublicSettings = {
  region: string
  setRegion: (value: string) => void
  currency: string
  setCurrency: (value: string) => void
}

const SettingsContext = createContext<PublicSettings | null>(null)

function stored(key: string, fallback: string) {
  const value = localStorage.getItem(key)
  return value && /^[A-Z]{2,3}$/.test(value) ? value : fallback
}

export function PublicSettingsProvider({ children }: { children: ReactNode }) {
  const [region, setRegionState] = useState(() => stored('md-public-region', 'CN'))
  const [currency, setCurrencyState] = useState(() => stored('md-public-currency', 'CNY'))
  const value = useMemo<PublicSettings>(() => ({
    region,
    setRegion: (next) => {
      const normalized = next.trim().toUpperCase()
      if (/^[A-Z]{2}$/.test(normalized)) {
        localStorage.setItem('md-public-region', normalized)
        setRegionState(normalized)
      }
    },
    currency,
    setCurrency: (next) => {
      const normalized = next.trim().toUpperCase()
      if (/^[A-Z]{3}$/.test(normalized)) {
        localStorage.setItem('md-public-currency', normalized)
        setCurrencyState(normalized)
      }
    },
  }), [currency, region])
  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>
}

export function usePublicSettings() {
  const value = useContext(SettingsContext)
  if (!value) throw new Error('usePublicSettings must be used inside PublicSettingsProvider')
  return value
}

