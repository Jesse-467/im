import { post } from './client'
import type { Profile, LoginResult } from './types'

/** Account 服务 API（默认 http://127.0.0.1:8001） */

export function apiRegister(input: {
  email: string
  password: string
  nickName: string
  gender: number
}): Promise<Record<string, never>> {
  return post('account', '/api/user/register', input)
}

export function apiLogin(email: string, password: string): Promise<LoginResult> {
  return post('account', '/api/user/login', { email, password })
}

export function apiPersonalInfo(): Promise<Profile> {
  return post('account', '/api/user/personal_info', {})
}

export function apiQueryUserInfo(userId: number): Promise<Profile> {
  return post('account', '/api/user/query_user_info', { userId })
}

export function apiModifyPersonalInfo(input: {
  nickName: string
  gender: number
  avatarUrl: string
}): Promise<{ success: boolean }> {
  return post('account', '/api/user/modify_personal_info', input)
}
