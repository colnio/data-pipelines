import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, setToken, clearToken } from './client'
import type { components } from './schema.d'

// ─── Type aliases ────────────────────────────────────────────────────────────

export type Run = components['schemas']['Run']
export type RunFile = components['schemas']['RunFile']
export type RunStateTransition = components['schemas']['RunStateTransition']
export type UserProfile = components['schemas']['UserProfile']
export type ReviewListItem = components['schemas']['ReviewListItem']
export type ReviewArtifactFull = components['schemas']['ReviewArtifactFull']

export interface ListRunsParams {
  state?: string
  sample_id?: string
  device_id?: string
  measurement_type?: string
  agent_id?: string
  condition_label?: string
  declared_after?: string
  declared_before?: string
  publication_status?: string
  cursor?: string
  limit?: number
}

// ─── Query keys ──────────────────────────────────────────────────────────────

export const queryKeys = {
  runs: (params?: ListRunsParams) => ['runs', params] as const,
  run: (id: string) => ['runs', id] as const,
  reviews: () => ['reviews'] as const,
  review: (runId: string) => ['reviews', runId] as const,
  me: () => ['me'] as const,
}

// ─── Auth ────────────────────────────────────────────────────────────────────

export function useLoginMutation() {
  return useMutation({
    mutationFn: async (vars: { email: string; password: string }) => {
      const data = await api.post<components['schemas']['AuthLoginOutputBody_ece96b00']>(
        '/v1/auth/login',
        vars,
      )
      setToken(data.access_token)
      return data
    },
  })
}

// ─── Runs ────────────────────────────────────────────────────────────────────

export function useRunsQuery(params?: ListRunsParams) {
  const search = new URLSearchParams()
  if (params?.state) search.set('state', params.state)
  if (params?.sample_id) search.set('sample_id', params.sample_id)
  if (params?.device_id) search.set('device_id', params.device_id)
  if (params?.measurement_type) search.set('measurement_type', params.measurement_type)
  if (params?.agent_id) search.set('agent_id', params.agent_id)
  if (params?.condition_label) search.set('condition_label', params.condition_label)
  if (params?.declared_after) search.set('declared_after', params.declared_after)
  if (params?.declared_before) search.set('declared_before', params.declared_before)
  if (params?.publication_status) search.set('publication_status', params.publication_status)
  if (params?.cursor) search.set('cursor', params.cursor)
  if (params?.limit != null) search.set('limit', String(params.limit))
  const qs = search.toString() ? `?${search.toString()}` : ''

  return useQuery({
    queryKey: queryKeys.runs(params),
    queryFn: () =>
      api.get<components['schemas']['ListRunsOutputBody_e3aaf148']>(`/v1/runs${qs}`),
  })
}

export function useRunQuery(id: string) {
  return useQuery({
    queryKey: queryKeys.run(id),
    queryFn: () =>
      api.get<components['schemas']['GetRunOutputBody_87da3ba3']>(`/v1/runs/${id}`),
    enabled: Boolean(id),
  })
}

// ─── Reviews ─────────────────────────────────────────────────────────────────

export function useReviewsQuery() {
  return useQuery({
    queryKey: queryKeys.reviews(),
    queryFn: () =>
      api.get<components['schemas']['ReviewListOutputBody_9a2fb657']>('/v1/reviews'),
  })
}

export function useReviewQuery(runId: string) {
  return useQuery({
    queryKey: queryKeys.review(runId),
    queryFn: () =>
      api.get<components['schemas']['ReviewGetOutputBody_b610a7a6']>(`/v1/reviews/${runId}`),
    enabled: Boolean(runId),
  })
}

// ─── Review mutations ────────────────────────────────────────────────────────

export function useApproveMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (runId: string) =>
      api.post<components['schemas']['ReviewApproveOutputBody_7d680067']>(
        `/v1/reviews/${runId}/approve`,
      ),
    onSuccess: (_data, runId) => {
      void qc.invalidateQueries({ queryKey: queryKeys.reviews() })
      void qc.invalidateQueries({ queryKey: queryKeys.review(runId) })
    },
  })
}

export function useRequestChangesMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ runId, reason }: { runId: string; reason: string }) =>
      api.post<components['schemas']['ReviewRequestChangesOutputBody_54d47f73']>(
        `/v1/reviews/${runId}/request-changes`,
        { reason },
      ),
    onSuccess: (_data, { runId }) => {
      void qc.invalidateQueries({ queryKey: queryKeys.reviews() })
      void qc.invalidateQueries({ queryKey: queryKeys.review(runId) })
    },
  })
}

export function useQuarantineMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ runId, reason }: { runId: string; reason: string }) =>
      api.post<components['schemas']['ReviewQuarantineOutputBody_54d47f73']>(
        `/v1/reviews/${runId}/quarantine`,
        { reason },
      ),
    onSuccess: (_data, { runId }) => {
      void qc.invalidateQueries({ queryKey: queryKeys.reviews() })
      void qc.invalidateQueries({ queryKey: queryKeys.review(runId) })
    },
  })
}

export function usePatchMetadataMutation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      runId,
      operator_comment,
    }: {
      runId: string
      operator_comment: string
    }) =>
      api.patch<components['schemas']['ReviewPatchMetadataOutputBody_e4cbab6f']>(
        `/v1/reviews/${runId}/metadata`,
        { operator_comment },
      ),
    onSuccess: (_data, { runId }) => {
      void qc.invalidateQueries({ queryKey: queryKeys.review(runId) })
    },
  })
}

// ─── Auth context helper ──────────────────────────────────────────────────────

export function useLogout() {
  const qc = useQueryClient()
  return () => {
    clearToken()
    qc.clear()
    window.location.href = '/login'
  }
}
