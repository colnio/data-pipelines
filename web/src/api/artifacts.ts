import { useQuery } from '@tanstack/react-query'
import { api } from './client'

// ─── Types ────────────────────────────────────────────────────────────────────

export interface Artifact {
  id: number
  kind: string
  filename: string
  sha256: string
  /** Pre-signed relative URL — use directly in <img src> or plain fetch; do NOT add auth header */
  url: string
}

interface ListArtifactsResponse {
  artifacts: Artifact[]
}

// ─── Query key ────────────────────────────────────────────────────────────────

export const artifactKeys = {
  runArtifacts: (runId: string) => ['artifacts', 'run', runId] as const,
}

// ─── Hook ─────────────────────────────────────────────────────────────────────

export function useRunArtifactsQuery(runId: string) {
  return useQuery({
    queryKey: artifactKeys.runArtifacts(runId),
    queryFn: () => api.get<ListArtifactsResponse>(`/v1/runs/${runId}/artifacts`),
    enabled: Boolean(runId),
  })
}
