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

type RawProfile = Omit<Profile, 'userId'> & { userId: string | number }
type RawLoginResult = Omit<LoginResult, 'userId'> & { userId: string | number }

function normalizeProfile(profile: RawProfile): Profile {
  return { ...profile, userId: String(profile.userId) }
}

export function apiLogin(
  email: string,
  password: string,
  deviceId: string,
  platform = 'desktop'
): Promise<LoginResult> {
  return post<RawLoginResult>('account', '/api/user/login', {
    email,
    password,
    deviceId,
    platform
  }).then((res) => ({ ...res, userId: String(res.userId) }))
}

export function apiLogout(deviceId: string): Promise<{ success: boolean }> {
  return post('account', '/api/user/logout', { deviceId })
}

export function apiPersonalInfo(): Promise<Profile> {
  return post<RawProfile>('account', '/api/user/personal_info', {}).then(normalizeProfile)
}

export function apiQueryUserInfo(userId: string): Promise<Profile> {
  return post<RawProfile>('account', '/api/user/query_user_info', { userId }).then(normalizeProfile)
}

export function apiModifyPersonalInfo(input: {
  nickName: string
  gender: number
  avatarUrl: string
}): Promise<{ success: boolean }> {
  return post('account', '/api/user/modify_personal_info', input)
}
