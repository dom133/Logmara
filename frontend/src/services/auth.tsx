import React, { createContext, useContext, useState, useEffect, ReactNode } from 'react'
import { api } from './api'

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
  login: (username: string, password: string) => Promise<boolean>
  logout: () => void
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
      setToken(res.data.token)
      localStorage.setItem('token', res.data.token)
      setUser(res.data.user)
      return true
    } catch {
      return false
    }
  }

  const logout = () => {
    setToken(null)
    setUser(null)
    localStorage.removeItem('token')
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