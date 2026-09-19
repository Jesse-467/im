import { create } from 'zustand'
import {
  apiLogin,
  apiLogout,
  apiModifyPersonalInfo,
  apiPersonalInfo,
  apiRegister
} from '@/api/account'
import { ApiError, CODE_TOKEN_REVOKED, CODE_UNAUTHORIZED } from '@/api/client'
import { setToken, getDeviceId, getToken } from '@/core/session'
import { wsClient } from '@/core/ws'
import type { Profile } from '@/api/types'
import { toast } from './toast'

export type AuthStatus = 'boot' | 'guest' | 'authed'

interface AuthState {
  status: AuthStatus
  token: string
  userId: string
  profile: Profile | null

  bootstrap: () => Promise<void>
  login: (email: string, password: string) => Promise<void>
  register: (input: {
    email: string
    password: string
    nickName: string
    gender: number
  }) => Promise<void>
  logout: () => void
  loadProfile: () => Promise<void>
  updateProfile: (input: { nickName: string; gender: number; avatarUrl: string }) => Promise<void>
}

export const useAuthStore = create<AuthState>((set, get) => ({
  status: 'boot',
  token: '',
  userId: '',
  profile: null,

  bootstrap: async () => {
    const cached = getToken()
    if (!cached) {
      set({ status: 'guest' })
      return
    }
    set({ token: cached, status: 'authed' })
    try {
      await get().loadProfile()
    } catch (err) {
      if (err instanceof ApiError && (err.code === CODE_UNAUTHORIZED || err.code === CODE_TOKEN_REVOKED)) {
        get().logout()
      }
    }
  },

  login: async (email, password) => {
    const res = await apiLogin(email, password, getDeviceId(), 'desktop')
    setToken(res.accessToken)
    set({ token: res.accessToken, userId: res.userId, status: 'authed' })
    await get().loadProfile().catch(() => undefined)
  },

  register: async (input) => {
    await apiRegister(input)
    // 注册成功后直接自动登录，减少一步跳转
    await get().login(input.email, input.password)
    toast('注册成功，已自动登录', 'success')
  },

  logout: () => {
    if (getToken()) void apiLogout(getDeviceId()).catch(() => undefined)
    wsClient.close()
    setToken('')
    set({ status: 'guest', token: '', userId: '', profile: null })
  },

  loadProfile: async () => {
    const profile = await apiPersonalInfo()
    set({ profile, userId: profile.userId })
  },

  updateProfile: async (input) => {
    await apiModifyPersonalInfo(input)
    const profile = await apiPersonalInfo()
    set({ profile })
    toast('资料已更新', 'success')
  }
}))
