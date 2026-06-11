import React, { createContext, useContext, useEffect, useState } from 'react'
import { api, getToken, setToken, clearToken } from '@/api/client'
import type { components } from '@/api/schema.d'

type UserProfile = components['schemas']['UserProfile']

type AuthStatus = 'loading' | 'authenticated' | 'unauthenticated'

interface AuthContextValue {
  status: AuthStatus
  user: UserProfile | null
  login: (email: string, password: string) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>('loading')
  const [user, setUser] = useState<UserProfile | null>(null)

  // On mount: if we have a stored token, validate it by fetching /v1/auth/me
  useEffect(() => {
    const token = getToken()
    if (!token) {
      setStatus('unauthenticated')
      return
    }
    api
      .get<components['schemas']['AuthMeOutputBody_1b20e166']>('/v1/auth/me')
      .then((data) => {
        setUser(data.user)
        setStatus('authenticated')
      })
      .catch(() => {
        clearToken()
        setStatus('unauthenticated')
      })
  }, [])

  const login = async (email: string, password: string) => {
    const data = await api.post<components['schemas']['AuthLoginOutputBody_ece96b00']>(
      '/v1/auth/login',
      { email, password },
    )
    setToken(data.access_token)
    setUser(data.user)
    setStatus('authenticated')
  }

  const logout = () => {
    clearToken()
    setUser(null)
    setStatus('unauthenticated')
    window.location.href = '/login'
  }

  return (
    <AuthContext.Provider value={{ status, user, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider')
  return ctx
}
