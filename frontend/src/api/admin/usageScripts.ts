/**
 * Admin Usage Scripts API endpoints
 * Handles Starlark usage script management for administrators
 */

import { apiClient } from '../client'

export interface UsageScript {
  id: number
  base_url_host: string
  account_type: string
  script: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface CreateUsageScriptRequest {
  base_url_host: string
  account_type: string
  script: string
  enabled?: boolean
}

export interface UpdateUsageScriptRequest {
  base_url_host: string
  account_type: string
  script: string
  enabled?: boolean
}

export async function list(): Promise<UsageScript[]> {
  const { data } = await apiClient.get<UsageScript[]>('/admin/usage-scripts')
  return data
}

export async function create(req: CreateUsageScriptRequest): Promise<UsageScript> {
  const { data } = await apiClient.post<UsageScript>('/admin/usage-scripts', req)
  return data
}

export async function update(id: number, req: UpdateUsageScriptRequest): Promise<UsageScript> {
  const { data } = await apiClient.put<UsageScript>(`/admin/usage-scripts/${id}`, req)
  return data
}

export async function deleteScript(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/usage-scripts/${id}`)
  return data
}

export async function toggleEnabled(id: number, script: UsageScript): Promise<UsageScript> {
  return update(id, {
    base_url_host: script.base_url_host,
    account_type: script.account_type,
    script: script.script,
    enabled: !script.enabled
  })
}

export const usageScriptsAPI = {
  list,
  create,
  update,
  delete: deleteScript,
  toggleEnabled
}

export default usageScriptsAPI
