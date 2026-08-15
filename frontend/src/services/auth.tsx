import React, { createContext, useContext, useState, useEffect, ReactNode } from 'react'
import { api, refreshAccessToken } from './api'

interface User {
  id: number
  username: string
  role: string
  is_admin: boolean
  is_active: boolean
}

interface AuthContextType {
  token: string | null
  user: User | null
  login: (username: string, password: string) => Promise<{ ok: boolean; error?: string }>
  logout: () => Promise<void>
  isAdmin: boolean
}

const AuthContext = createContext<AuthContextType>({} as AuthContextType)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('token'))
  const [user, setUser] = useState<User | null>(null)

  useEffect(() => {
    if (token) {
      api.defaults.headers.common['Authorization'] = `Bearer ${token}`
      loadUser()
    }
  }, [token])

  const loadUser = async () => {
    try {
      const res = await api.get('/auth/me')
      setUser(res.data)
    } catch {
      logout()
    }
  }

  const login = async (username: string, password: string) => {
    try {
      const res = await api.post('/auth/login', { username, password })
      const t = res.data.token
      const rt = res.data.refresh_token
      setToken(t)
      localStorage.setItem('token', t)
      localStorage.setItem('refresh_token', rt)
      setUser(res.data.user)
      return { ok: true }
    } catch (e: any) {
      return {
        ok: false,
        error: e.response?.data?.error || 'Invalid credentials',
      }
    }
  }

  const logout = async () => {
    const rt = localStorage.getItem('refresh_token')
    if (rt) {
      try {
        await api.post('/auth/logout', { refresh_token: rt })
      } catch { /* ignore */ }
    }
    setToken(null)
    setUser(null)
    localStorage.removeItem('token')
    localStorage.removeItem('refresh_token')
    delete api.defaults.headers.common['Authorization']
  }

  return (
    <AuthContext.Provider value={{ token, user, login, logout, isAdmin: user?.role === 'admin' }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  return useContext(AuthContext)
}