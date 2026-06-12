import { useState } from 'react'
import {
  Box,
  Button,
  Group,
  Select,
  Table,
  Text,
  TextInput,
  Title,
  Alert,
  Loader,
  Anchor,
  Stack,
} from '@mantine/core'
import { Link } from '@tanstack/react-router'
import { useRunsQuery, type ListRunsParams } from '@/api/queries'
import { StateBadge } from '@/components/StateBadge'

const STATE_OPTIONS = [
  { value: '', label: 'All states' },
  { value: 'declared', label: 'declared' },
  { value: 'processing', label: 'processing' },
  { value: 'awaiting_review', label: 'awaiting_review' },
  { value: 'approved', label: 'approved' },
  { value: 'changes_requested', label: 'changes_requested' },
  { value: 'quarantined', label: 'quarantined' },
  { value: 'published', label: 'published' },
]

const PUB_STATUS_OPTIONS = [
  { value: '', label: 'All' },
  { value: 'published', label: 'published' },
  { value: 'unpublished', label: 'unpublished' },
]

interface PendingFilters {
  state?: string
  sample_id?: string
  device_id?: string
  measurement_type?: string
  condition_label?: string
  publication_status?: string
  declared_after_date?: string   // YYYY-MM-DD from <input type="date">
  declared_before_date?: string
}

function toRfc3339Start(d: string): string {
  return `${d}T00:00:00Z`
}
function toRfc3339End(d: string): string {
  return `${d}T00:00:00Z`
}

function pendingToParams(p: PendingFilters): ListRunsParams {
  const params: ListRunsParams = {}
  if (p.state) params.state = p.state
  if (p.sample_id) params.sample_id = p.sample_id
  if (p.device_id) params.device_id = p.device_id
  if (p.measurement_type) params.measurement_type = p.measurement_type
  if (p.condition_label) params.condition_label = p.condition_label
  if (p.publication_status) params.publication_status = p.publication_status
  if (p.declared_after_date) params.declared_after = toRfc3339Start(p.declared_after_date)
  if (p.declared_before_date) params.declared_before = toRfc3339End(p.declared_before_date)
  return params
}

export function RunsPage() {
  const [pending, setPending] = useState<PendingFilters>({})
  const [filters, setFilters] = useState<ListRunsParams>({})
  const [cursor, setCursor] = useState<string | undefined>(undefined)

  const queryParams: ListRunsParams = { ...filters, cursor }

  const { data, isLoading, isError, error } = useRunsQuery(queryParams)

  const runs = data?.runs ?? []

  const apply = () => {
    setCursor(undefined)
    setFilters(pendingToParams(pending))
  }

  const reset = () => {
    setPending({})
    setFilters({})
    setCursor(undefined)
  }

  return (
    <Box>
      <Title order={3} mb="md">
        Runs
      </Title>

      {/* Filter bar */}
      <Stack gap="xs" mb="md">
        <Group align="flex-end" wrap="wrap">
          <Select
            label="State"
            data={STATE_OPTIONS}
            value={pending.state ?? ''}
            onChange={(v) =>
              setPending((f) => ({ ...f, state: v ?? undefined }))
            }
            clearable
            w={180}
          />
          <TextInput
            label="Sample ID"
            value={pending.sample_id ?? ''}
            onChange={(e) =>
              setPending((f) => ({
                ...f,
                sample_id: e.currentTarget.value || undefined,
              }))
            }
            w={180}
          />
          <TextInput
            label="Device ID"
            value={pending.device_id ?? ''}
            onChange={(e) =>
              setPending((f) => ({
                ...f,
                device_id: e.currentTarget.value || undefined,
              }))
            }
            w={180}
          />
          <TextInput
            label="Measurement type"
            value={pending.measurement_type ?? ''}
            onChange={(e) =>
              setPending((f) => ({
                ...f,
                measurement_type: e.currentTarget.value || undefined,
              }))
            }
            w={200}
          />
        </Group>
        <Group align="flex-end" wrap="wrap">
          <TextInput
            label="Condition label"
            value={pending.condition_label ?? ''}
            onChange={(e) =>
              setPending((f) => ({
                ...f,
                condition_label: e.currentTarget.value || undefined,
              }))
            }
            w={180}
          />
          <Select
            label="Publication status"
            data={PUB_STATUS_OPTIONS}
            value={pending.publication_status ?? ''}
            onChange={(v) =>
              setPending((f) => ({ ...f, publication_status: v ?? undefined }))
            }
            clearable
            w={180}
          />
          <Box>
            <Text fz="sm" fw={500} mb={4}>
              Declared after
            </Text>
            <input
              type="date"
              value={pending.declared_after_date ?? ''}
              onChange={(e) =>
                setPending((f) => ({
                  ...f,
                  declared_after_date: e.currentTarget.value || undefined,
                }))
              }
              style={{
                height: 36,
                padding: '0 8px',
                border: '1px solid var(--mantine-color-gray-4)',
                borderRadius: 4,
                fontSize: 14,
              }}
            />
          </Box>
          <Box>
            <Text fz="sm" fw={500} mb={4}>
              Declared before
            </Text>
            <input
              type="date"
              value={pending.declared_before_date ?? ''}
              onChange={(e) =>
                setPending((f) => ({
                  ...f,
                  declared_before_date: e.currentTarget.value || undefined,
                }))
              }
              style={{
                height: 36,
                padding: '0 8px',
                border: '1px solid var(--mantine-color-gray-4)',
                borderRadius: 4,
                fontSize: 14,
              }}
            />
          </Box>
          <Button onClick={apply}>Apply</Button>
          <Button variant="subtle" color="gray" onClick={reset}>
            Reset
          </Button>
        </Group>
      </Stack>

      {isLoading && <Loader size="sm" />}
      {isError && (
        <Alert color="red">
          {error instanceof Error ? error.message : 'Failed to load runs'}
        </Alert>
      )}

      {!isLoading && !isError && (
        <>
          <Table striped highlightOnHover withTableBorder data-testid="runs-table">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Run ID</Table.Th>
                <Table.Th>State</Table.Th>
                <Table.Th>Measurement type</Table.Th>
                <Table.Th>Sample</Table.Th>
                <Table.Th>Device</Table.Th>
                <Table.Th>Declared at</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {runs.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={6}>
                    <Text c="dimmed" fz="sm" ta="center" py="md">
                      No runs found
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )}
              {runs.map((run) => (
                <Table.Tr key={run.id}>
                  <Table.Td>
                    <Link to="/runs/$id" params={{ id: run.id }}>
                      <Anchor component="span" fz="sm">
                        {run.id.slice(0, 8)}…
                      </Anchor>
                    </Link>
                  </Table.Td>
                  <Table.Td>
                    <StateBadge state={run.state} />
                  </Table.Td>
                  <Table.Td fz="sm">{run.measurement_type}</Table.Td>
                  <Table.Td fz="sm">{run.sample_id ?? '—'}</Table.Td>
                  <Table.Td fz="sm">{run.device_id ?? '—'}</Table.Td>
                  <Table.Td fz="sm">
                    {new Date(run.declared_at).toLocaleString()}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>

          {/* Pagination */}
          <Group mt="md">
            {data?.next_cursor && (
              <Button
                variant="subtle"
                onClick={() => setCursor(data.next_cursor)}
                loading={isLoading}
              >
                Next
              </Button>
            )}
            {cursor !== undefined && (
              <Button
                variant="subtle"
                color="gray"
                onClick={() => setCursor(undefined)}
              >
                Reset
              </Button>
            )}
          </Group>
        </>
      )}
    </Box>
  )
}
