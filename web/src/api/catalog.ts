import { useQuery } from '@tanstack/react-query'
import { api } from './client'
import type { components } from './schema.d'

// ─── Type aliases ─────────────────────────────────────────────────────────────

export type Sample = components['schemas']['Sample']
export type Device = components['schemas']['Device']
export type ContactConfig = components['schemas']['ContactConfig']

// ─── Response interfaces ───────────────────────────────────────────────────────

interface ListSamplesResponse {
  samples: Sample[] | null
  next_cursor?: string
}

interface GetSampleResponse {
  sample: Sample
}

interface ListDevicesResponse {
  devices: Device[] | null
  next_cursor?: string
}

interface GetDeviceResponse {
  device: Device
}

interface ListContactConfigsResponse {
  contact_configs: ContactConfig[] | null
  next_cursor?: string
}

// ─── Query keys ───────────────────────────────────────────────────────────────

export const catalogKeys = {
  samples: (cursor?: string) => ['catalog', 'samples', cursor] as const,
  sample: (id: string) => ['catalog', 'samples', id] as const,
  devices: (sampleId?: string, cursor?: string) =>
    ['catalog', 'devices', sampleId, cursor] as const,
  device: (id: string) => ['catalog', 'devices', id] as const,
  contactConfigs: (deviceId?: string, cursor?: string) =>
    ['catalog', 'contact-configs', deviceId, cursor] as const,
}

// ─── Hooks ────────────────────────────────────────────────────────────────────

export function useSamplesQuery(cursor?: string) {
  const qs = cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''
  return useQuery({
    queryKey: catalogKeys.samples(cursor),
    queryFn: () => api.get<ListSamplesResponse>(`/v1/samples${qs}`),
  })
}

export function useSampleQuery(id: string) {
  return useQuery({
    queryKey: catalogKeys.sample(id),
    queryFn: () => api.get<GetSampleResponse>(`/v1/samples/${id}`),
    enabled: Boolean(id),
  })
}

export function useDevicesQuery(sampleId?: string, cursor?: string) {
  const search = new URLSearchParams()
  if (sampleId) search.set('sample_id', sampleId)
  if (cursor) search.set('cursor', cursor)
  const qs = search.toString() ? `?${search.toString()}` : ''
  return useQuery({
    queryKey: catalogKeys.devices(sampleId, cursor),
    queryFn: () => api.get<ListDevicesResponse>(`/v1/devices${qs}`),
  })
}

export function useDeviceQuery(id: string) {
  return useQuery({
    queryKey: catalogKeys.device(id),
    queryFn: () => api.get<GetDeviceResponse>(`/v1/devices/${id}`),
    enabled: Boolean(id),
  })
}

export function useContactConfigsQuery(deviceId?: string, cursor?: string) {
  const search = new URLSearchParams()
  if (deviceId) search.set('device_id', deviceId)
  if (cursor) search.set('cursor', cursor)
  const qs = search.toString() ? `?${search.toString()}` : ''
  return useQuery({
    queryKey: catalogKeys.contactConfigs(deviceId, cursor),
    queryFn: () => api.get<ListContactConfigsResponse>(`/v1/contact-configs${qs}`),
  })
}
