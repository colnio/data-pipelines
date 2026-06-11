import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MantineProvider } from '@mantine/core'
import { createRouter, createRootRoute, createRoute, RouterProvider, Outlet } from '@tanstack/react-router'
import { RunsPage } from '@/pages/RunsPage'

// Minimal router wrapping RunsPage so TanStack Router Link works
function makeTestRouter() {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const runsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/', component: RunsPage })
  const runDetailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/runs/$id',
    component: () => <div>run detail</div>,
  })
  const routeTree = rootRoute.addChildren([runsRoute, runDetailRoute])
  return createRouter({ routeTree })
}

const mockRuns = [
  {
    id: 'run-uuid-0001-aabbccddeeff',
    state: 'awaiting_review',
    measurement_type: 'iv_curve',
    sample_id: 'S-001',
    device_id: 'D-001',
    agent_id: 'agent-1',
    declared_at: '2024-01-15T10:00:00Z',
    declared_by: 'agent-1',
    completion_source: 'agent',
    manifest_hash: 'sha256abc',
    meas_path: '/data/run-001',
    operator_comment: '',
    created_at: '2024-01-15T10:00:00Z',
    updated_at: '2024-01-15T10:00:00Z',
  },
  {
    id: 'run-uuid-0002-1122334455ff',
    state: 'approved',
    measurement_type: 'cv_sweep',
    sample_id: 'S-002',
    device_id: 'D-002',
    agent_id: 'agent-2',
    declared_at: '2024-01-16T10:00:00Z',
    declared_by: 'agent-2',
    completion_source: 'agent',
    manifest_hash: 'sha256def',
    meas_path: '/data/run-002',
    operator_comment: '',
    created_at: '2024-01-16T10:00:00Z',
    updated_at: '2024-01-16T10:00:00Z',
  },
]

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

function TestApp() {
  const qc = makeClient()
  const testRouter = makeTestRouter()
  return (
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <RouterProvider router={testRouter} />
      </QueryClientProvider>
    </MantineProvider>
  )
}

describe('RunsPage', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ runs: mockRuns }),
      }),
    )
    vi.stubGlobal('localStorage', {
      getItem: () => 'fake-token',
      setItem: vi.fn(),
      removeItem: vi.fn(),
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the Runs heading', async () => {
    render(<TestApp />)
    await waitFor(() => {
      expect(screen.getByText('Runs')).toBeInTheDocument()
    })
  })

  it('renders rows from the mocked /v1/runs response', async () => {
    render(<TestApp />)

    await waitFor(() => {
      expect(screen.getByTestId('runs-table')).toBeInTheDocument()
    })

    // Both measurement types should appear
    expect(screen.getByText('iv_curve')).toBeInTheDocument()
    expect(screen.getByText('cv_sweep')).toBeInTheDocument()

    // State badges (may appear multiple times due to select options)
    expect(screen.getAllByText('awaiting review').length).toBeGreaterThan(0)
    expect(screen.getAllByText('approved').length).toBeGreaterThan(0)
  })

  it('shows "No runs found" when the list is empty', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ runs: [] }),
      }),
    )
    render(<TestApp />)
    await waitFor(() => {
      expect(screen.getByText('No runs found')).toBeInTheDocument()
    })
  })
})
