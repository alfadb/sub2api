/**
 * Admin Copilot API endpoints
 * Handles GitHub Copilot Device Code OAuth flow for administrators
 */

import { apiClient } from '../client'

export interface DeviceCodeResponse {
  device_code: string
  user_code: string
  verification_uri: string
  interval: number
  expires_in: number
}

export interface PollTokenResponse {
  status: 'pending' | 'success' | 'slow_down' | 'expired' | 'error'
  access_token?: string
  error?: string
}

export interface CopilotDefaultModelMapping {
  [key: string]: string
}

export async function initiateDeviceCode(): Promise<DeviceCodeResponse> {
  const { data } = await apiClient.post<DeviceCodeResponse>(
    '/admin/copilot/oauth/device-code'
  )
  return data
}

export async function pollToken(deviceCode: string): Promise<PollTokenResponse> {
  const { data } = await apiClient.post<PollTokenResponse>(
    '/admin/copilot/oauth/poll-token',
    { device_code: deviceCode }
  )
  return data
}

export async function getDefaultModelMapping(): Promise<CopilotDefaultModelMapping> {
  const { data } = await apiClient.get<CopilotDefaultModelMapping>(
    '/admin/copilot/default-model-mapping'
  )
  return data
}

export default { initiateDeviceCode, pollToken, getDefaultModelMapping }
