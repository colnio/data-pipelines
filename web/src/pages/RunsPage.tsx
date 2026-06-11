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

export function RunsPage() {
  const [filters, setFilters] = useState<ListRunsParams>({})
  const [pendingFilters, setPendingFilters] = useState<ListRunsParams>({})

  const { data, isLoading, isError, error } = useRunsQuery(filters)

  const runs = data?.runs ?? []

  const apply = () => setFilters({ ...pendingFilters })
  const reset = () => {
    setPendingFilters({})
    setFilters({})
  }

  return (
    <Box>
      <Title order={3} mb="md">
        Runs
      </Title>

      {/* Filter bar */}
      <Group mb="md" align="flex-end" wrap="wrap">
        <Select
          label="State"
          data={STATE_OPTIONS}
          value={pendingFilters.state ?? ''}
          onChange={(v) =>
            setPendingFilters((f) => ({ ...f, state: v ?? undefined }))
          }
          clearable
          w={180}
        />
        <TextInput
          label="Sample ID"
          value={pendingFilters.sample_id ?? ''}
          onChange={(e) =>
            setPendingFilters((f) => ({
              ...f,
              sample_id: e.currentTarget.value || undefined,
            }))
          }
          w={180}
        />
        <TextInput
          label="Device ID"
          value={pendingFilters.device_id ?? ''}
          onChange={(e) =>
            setPendingFilters((f) => ({
              ...f,
              device_id: e.currentTarget.value || undefined,
            }))
          }
          w={180}
        />
        <TextInput
          label="Measurement type"
          value={pendingFilters.measurement_type ?? ''}
          onChange={(e) =>
            setPendingFilters((f) => ({
              ...f,
              measurement_type: e.currentTarget.value || undefined,
            }))
          }
          w={200}
        />
        <Button onClick={apply}>Apply</Button>
        <Button variant="subtle" color="gray" onClick={reset}>
          Reset
        </Button>
      </Group>

      {isLoading && <Loader size="sm" />}
      {isError && (
        <Alert color="red">
          {error instanceof Error ? error.message : 'Failed to load runs'}
        </Alert>
      )}

      {!isLoading && !isError && (
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
      )}
    </Box>
  )
}
