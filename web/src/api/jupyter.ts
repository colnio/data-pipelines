import { useQuery, useMutation } from '@tanstack/react-query'
import { api } from './client'

// ─── Response interfaces ──────────────────────────────────────────────────────

export interface JupyterInfo {
  hub_url: string
  reachable: boolean
  checked_at: string
}

export interface JupyterConnection {
  server_url: string
  token: string
  vscode_server_uri: string
  hub_url: string
  username: string
  expires_at: string
}

// ─── Query keys ───────────────────────────────────────────────────────────────

export const jupyterQueryKeys = {
  info: () => ['jupyter', 'info'] as const,
}

// ─── Hooks ────────────────────────────────────────────────────────────────────

export function useJupyterInfoQuery() {
  return useQuery({
    queryKey: jupyterQueryKeys.info(),
    queryFn: () => api.get<JupyterInfo>('/v1/jupyter/info'),
  })
}

export function useJupyterConnectionMutation() {
  return useMutation({
    mutationFn: () => api.post<JupyterConnection>('/v1/jupyter/connection'),
  })
}
