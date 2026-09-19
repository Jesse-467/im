import { create } from 'zustand'
import { apiLogin, apiModifyPersonalInfo, apiPersonalInfo, apiRegister } from '@/api/account'
import { ApiError, CODE_UNAUTHORIZED } from '@/api/client'
import { setToken, getToken } from '@/core/session'
import type { Profile } from '@/api/types'
import { toast } from './toast'

export type AuthStatus = 'boot' | 'guest' | 'authed'

interface AuthState {
  status: AuthStatus
  token: string
  userId: number
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
  userId: 0,
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
      if (err instanceof ApiError && err.code === CODE_UNAUTHORIZED) {
        get().logout()
      }
    }
  },

  login: async (email, password) => {
    const res = await apiLogin(email, password)
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
    setToken('')
    set({ status: 'guest', token: '', userId: 0, profile: null })
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
