/**
 * Admin API hooks and mutations.
 * Queries/mutations for Users, Agents, Jobs+Audit, and Notifications.
 */
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, getToken, ApiError } from './client'
import type { components } from './schema.d'

// ─── Type aliases ─────────────────────────────────────────────────────────────

export type AdminUserRow = components['schemas']['AdminUserRow']
export type AdminAgentRow = components['schemas']['AdminAgentRow']
export type AdminJobRow = components['schemas']['AdminJobRow']
export type AdminAuditRow = components['schemas']['AdminAuditRow']
export type NotificationRow = components['schemas']['NotificationRow']
export type NotifyConfigBody = components['schemas']['NotifyConfigBody']

// Local wrapper types (the huma body names are verbose)
export interface AdminUsersResponse { users: AdminUserRow[] | null }
export interface AdminAgentsResponse { agents: AdminAgentRow[] | null }
export interface AdminJobsResponse { jobs: AdminJobRow[] | null }
export interface AdminAuditResponse { transitions: AdminAuditRow[] | null }
export interface NotifyLogResponse { notifications: NotificationRow[] | null }

// ─── PUT helper (api client only exports get/post/patch) ──────────────────────

async function apiPut<T>(path: string, body: unknown): Promise<T> {
  const token = getToken()
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = `Bearer ${token}`

  const res = await fetch(path, {
    method: 'PUT',
    headers,
    body: JSON.stringify(body),
  })

  if (res.status === 401) {
    window.location.href = '/login'
    throw new ApiError(401, 'unauthorized', 'Session expired')
  }
  if (!res.ok) {
    let code = 'unknown_error'
    let message = `HTTP ${res.status}`
    try {
      const b = await res.json()
      code = b.code ?? code
      message = b.message ?? message
    } catch { /* ignore */ }
    throw new ApiError(res.status, code, message)
  }
  if (res.status === 204) return undefined as unknown as T
  return res.json() as Promise<T>
}

// ─── Query keys ───────────────────────────────────────────────────────────────

export const adminQueryKeys = {
  users: (params?: { status?: string; role?: string; limit?: number }) =>
    ['admin', 'users', params] as const,
  agents: () => ['admin', 'agents'] as const,
  jobs: (params?: { state?: string; job_type?: string; limit?: number }) =>
    ['admin', 'jobs', params] as const,
  audit: (params?: { run_id?: string; limit?: number }) =>
    ['admin', 'audit', params] as const,
  notifConfig: () => ['admin', 'notif', 'config'] as const,
  notifLog: (limit?: number) => ['admin', 'notif', 'log', limit] as const,
}

// ─── Users ────────────────────────────────────────────────────────────────────

export interface AdminUsersParams {
  status?: string
  role?: string
  limit?: number
}

export function useAdminUsersQuery(params?: AdminUsersParams) {
  const search = new URLSearchParams()
  if (params?.status) search.set('status', params.status)
  if (params?.role) search.set('role', params.role)
  if (params?.limit != null) search.set('limit', String(params.limit))
  const qs = search.toString() ? `?${search.toString()}` : ''

  return useQuery({
    queryKey: adminQueryKeys.users(params),
    queryFn: () => api.get<AdminUsersResponse>(`/v1/admin/users${qs}`),
  })
}

export function usePatchUserMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: { status?: string; global_role?: string } }) =>
      api.patch<components['schemas']['AdminPatchUserOutputBody_3abec444']>(
        `/v1/admin/users/${id}`,
        body,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['admin', 'users'] })
    },
  })
}

// ─── Agents ───────────────────────────────────────────────────────────────────

export function useAdminAgentsQuery() {
  return useQuery({
    queryKey: adminQueryKeys.agents(),
    queryFn: () => api.get<AdminAgentsResponse>('/v1/admin/agents'),
  })
}

export function useRegisterAgentMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { id: string; display_name: string; allowed_roots: string[] }) =>
      api.post<components['schemas']['AdminRegisterAgentOutputBody_a33cf55e']>(
        '/v1/admin/agents',
        body,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: adminQueryKeys.agents() })
    },
  })
}

export function useRotateAgentKeyMutation() {
  return useMutation({
    mutationFn: (agentId: string) =>
      api.post<components['schemas']['AdminRotateKeyOutputBody_c9525ea8']>(
        `/v1/admin/agents/${agentId}/rotate-key`,
      ),
  })
}

export function usePatchAgentMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string
      body: { enabled?: boolean; allowed_roots?: string[] | null }
    }) =>
      api.patch<components['schemas']['AdminPatchAgentOutputBody_8a981e73']>(
        `/v1/admin/agents/${id}`,
        body,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: adminQueryKeys.agents() })
    },
  })
}

// ─── Jobs ─────────────────────────────────────────────────────────────────────

export interface AdminJobsParams {
  state?: string
  job_type?: string
  limit?: number
}

export function useAdminJobsQuery(params?: AdminJobsParams) {
  const search = new URLSearchParams()
  if (params?.state) search.set('state', params.state)
  if (params?.job_type) search.set('job_type', params.job_type)
  if (params?.limit != null) search.set('limit', String(params.limit))
  const qs = search.toString() ? `?${search.toString()}` : ''

  return useQuery({
    queryKey: adminQueryKeys.jobs(params),
    queryFn: () => api.get<AdminJobsResponse>(`/v1/admin/jobs${qs}`),
    refetchInterval: 30_000,
  })
}

// ─── Audit ────────────────────────────────────────────────────────────────────

export interface AdminAuditParams {
  run_id?: string
  limit?: number
}

export function useAdminAuditQuery(params?: AdminAuditParams) {
  const search = new URLSearchParams()
  if (params?.run_id) search.set('run_id', params.run_id)
  if (params?.limit != null) search.set('limit', String(params.limit))
  const qs = search.toString() ? `?${search.toString()}` : ''

  return useQuery({
    queryKey: adminQueryKeys.audit(params),
    queryFn: () => api.get<AdminAuditResponse>(`/v1/admin/audit${qs}`),
  })
}

// ─── Notifications ────────────────────────────────────────────────────────────

export function useNotificationConfigQuery() {
  return useQuery({
    queryKey: adminQueryKeys.notifConfig(),
    queryFn: () => api.get<NotifyConfigBody>('/v1/admin/notifications/config'),
  })
}

export function usePutNotificationConfigMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: components['schemas']['NotifyConfigPutInputBody_21d119b2']) =>
      apiPut<NotifyConfigBody>('/v1/admin/notifications/config', body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: adminQueryKeys.notifConfig() })
    },
  })
}

export function useTestNotificationMutation() {
  return useMutation({
    mutationFn: () =>
      api.post<components['schemas']['NotifyTestOutputBody_30b82b05']>(
        '/v1/admin/notifications/test',
      ),
  })
}

export function useNotificationLogQuery(limit?: number) {
  const qs = limit != null ? `?limit=${limit}` : ''
  return useQuery({
    queryKey: adminQueryKeys.notifLog(limit),
    queryFn: () => api.get<NotifyLogResponse>(`/v1/admin/notifications/log${qs}`),
  })
}
