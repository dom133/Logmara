// Centralized design tokens — single source of truth for colors, spacing,
// radius, shadow, and timing values used across the UI.  Inline styles in
// components should reference these instead of magic strings.

export const tokens = {
  colors: {
    primary: '#1890ff',
    primaryLight: '#e6f7ff',
    primaryGradient: 'linear-gradient(135deg, #1890ff 0%, #36cfc9 100%)',
    primaryGradientHover: 'linear-gradient(135deg, #40a9ff 0%, #69d0cb 100%)',
    background: {
      light: '#fafafa',
      dark: '#141414',
    },
    sidebar: {
      light: '#fafafa',
      dark: '#1f1f1f',
    },
    activeNav: {
      light: '#e6f7ff',
      dark: '#2a2a2a',
    },
    error: '#ff4d4f',
    errorBg: '#fff2f0',
    // Stat card accent colors
    statBlue: '#1890ff',
    statGreen: '#3f8600',
    statRed: '#cf1322',
    statCyan: '#13c2c2',
    statPurple: '#722ed1',
  },

  spacing: {
    xs: 4,
    sm: 8,
    md: 16,
    lg: 24,
    xl: 32,
    xxl: 48,
  },

  borderRadius: {
    sm: 4,
    md: 8,
    lg: 12,
    xl: 16,
    full: 9999,
  },

  shadow: {
    card: '0 2px 8px rgba(0, 0, 0, 0.08)',
    cardHover: '0 4px 16px rgba(0, 0, 0, 0.12)',
    glass: '0 8px 32px rgba(0, 0, 0, 0.06)',
    elevated: '0 8px 24px rgba(0, 0, 0, 0.1)',
  },

  // Transition timing for micro-animations
  transition: {
    fast: '0.15s ease',
    normal: '0.2s ease',
    slow: '0.3s ease',
  },

  // Glassmorphism preset
  glass: {
    background: 'rgba(255, 255, 255, 0.72)',
    backgroundDark: 'rgba(31, 31, 31, 0.72)',
    blur: 'blur(12px)',
    border: '1px solid rgba(255, 255, 255, 0.18)',
    borderDark: '1px solid rgba(255, 255, 255, 0.06)',
  },

  // Custom scrollbar sizing
  scrollbar: {
    width: 6,
    track: 'transparent',
    thumb: 'rgba(0, 0, 0, 0.15)',
    thumbDark: 'rgba(255, 255, 255, 0.15)',
  },
} as const

export type Tokens = typeof tokens
