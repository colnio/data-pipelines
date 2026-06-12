import { useState } from 'react'
import {
  Alert,
  Badge,
  Group,
  Loader,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { useAdminJobsQuery, useAdminAuditQuery } from '@/api/admin'
import { StateBadge } from '@/components/StateBadge'

const JOB_STATE_OPTIONS = [
  { value: '', label: 'All states' },
  { value: 'available', label: 'available' },
  { value: 'running', label: 'running' },
  { value: 'retryable', label: 'retryable' },
  { value: 'discarded', label: 'discarded' },
  { value: 'completed', label: 'completed' },
  { value: 'cancelled', label: 'cancelled' },
]

const JOB_STATE_COLOR: Record<string, string> = {
  available: 'blue',
  running: 'cyan',
  retryable: 'orange',
  discarded: 'red',
  completed: 'green',
  cancelled: 'gray',
}

const ACTOR_COLOR: Record<string, string> = {
  user: 'blue',
  agent: 'teal',
  system: 'gray',
}

export function JobsTab() {
  const [jobState, setJobState] = useState('')
  const [jobType, setJobType] = useState('')
  const [auditRunId, setAuditRunId] = useState('')

  const {
    data: jobsData,
    isLoading: jobsLoading,
    isError: jobsError,
    error: jobsErr,
  } = useAdminJobsQuery({
    state: jobState || undefined,
    job_type: jobType || undefined,
  })

  const {
    data: auditData,
    isLoading: auditLoading,
    isError: auditError,
    error: auditErr,
  } = useAdminAuditQuery({
    run_id: auditRunId || undefined,
  })

  const jobs = jobsData?.jobs ?? []
  const transitions = auditData?.transitions ?? []

  return (
    <Stack gap="xl">
      {/* Jobs section */}
      <Stack gap="md">
        <Group align="flex-end" justify="space-between">
          <Title order={5}>Jobs queue</Title>
          <Text fz="xs" c="dimmed">
            Auto-refreshes every 30 s
          </Text>
        </Group>
        <Group align="flex-end" wrap="wrap">
          <Select
            label="State"
            data={JOB_STATE_OPTIONS}
            value={jobState}
            onChange={(v) => setJobState(v ?? '')}
            w={160}
            clearable
          />
          <TextInput
            label="Job type"
            placeholder="e.g. ingest"
            value={jobType}
            onChange={(e) => setJobType(e.currentTarget.value)}
            w={200}
          />
        </Group>

        {jobsLoading && <Loader size="sm" />}
        {jobsError && (
          <Alert color="red">
            {jobsErr instanceof Error ? jobsErr.message : 'Failed to load jobs'}
          </Alert>
        )}

        {!jobsLoading && !jobsError && (
          <Table striped highlightOnHover withTableBorder>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>ID</Table.Th>
                <Table.Th>Type</Table.Th>
                <Table.Th>Run ID</Table.Th>
                <Table.Th>State</Table.Th>
                <Table.Th>Attempts</Table.Th>
                <Table.Th>Last error</Table.Th>
                <Table.Th>Created</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {jobs.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={7}>
                    <Text c="dimmed" fz="sm" ta="center" py="md">
                      No jobs found
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )}
              {jobs.map((job) => (
                <Table.Tr key={job.id}>
                  <Table.Td fz="sm">{job.id}</Table.Td>
                  <Table.Td fz="sm">{job.job_type}</Table.Td>
                  <Table.Td fz="sm">{job.run_id ? `${job.run_id.slice(0, 8)}…` : '—'}</Table.Td>
                  <Table.Td>
                    <Badge
                      color={JOB_STATE_COLOR[job.state] ?? 'gray'}
                      variant="light"
                      size="sm"
                    >
                      {job.state}
                    </Badge>
                  </Table.Td>
                  <Table.Td fz="sm">
                    {job.attempt_count} / {job.max_attempts}
                  </Table.Td>
                  <Table.Td fz="sm" maw={240} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {job.last_error ?? '—'}
                  </Table.Td>
                  <Table.Td fz="sm">
                    {new Date(job.created_at).toLocaleString()}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>

      {/* Audit section */}
      <Stack gap="md">
        <Title order={5}>State audit log</Title>
        <Group align="flex-end" wrap="wrap">
          <TextInput
            label="Filter by run ID"
            placeholder="paste full UUID"
            value={auditRunId}
            onChange={(e) => setAuditRunId(e.currentTarget.value)}
            w={320}
          />
        </Group>

        {auditLoading && <Loader size="sm" />}
        {auditError && (
          <Alert color="red">
            {auditErr instanceof Error ? auditErr.message : 'Failed to load audit log'}
          </Alert>
        )}

        {!auditLoading && !auditError && (
          <Table striped highlightOnHover withTableBorder>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Run</Table.Th>
                <Table.Th>Transition</Table.Th>
                <Table.Th>Actor</Table.Th>
                <Table.Th>Reason</Table.Th>
                <Table.Th>When</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {transitions.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={5}>
                    <Text c="dimmed" fz="sm" ta="center" py="md">
                      No audit entries found
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )}
              {transitions.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Td fz="sm">{row.run_id.slice(0, 8)}…</Table.Td>
                  <Table.Td>
                    <Group gap={4}>
                      <StateBadge state={row.from_state} />
                      <Text fz="xs" c="dimmed">
                        →
                      </Text>
                      <StateBadge state={row.to_state} />
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Group gap={4}>
                      <Badge
                        color={ACTOR_COLOR[row.actor_type] ?? 'gray'}
                        variant="outline"
                        size="xs"
                      >
                        {row.actor_type}
                      </Badge>
                      <Text fz="xs" c="dimmed">
                        {row.actor_id.slice(0, 8)}
                      </Text>
                    </Group>
                  </Table.Td>
                  <Table.Td
                    fz="sm"
                    maw={240}
                    style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  >
                    {row.reason || '—'}
                  </Table.Td>
                  <Table.Td fz="sm">{new Date(row.created_at).toLocaleString()}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </Stack>
  )
}
