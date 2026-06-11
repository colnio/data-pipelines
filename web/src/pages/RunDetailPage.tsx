import { Box, Title, Text, Group, Table, Timeline, Alert, Loader, Stack, Divider, Anchor } from '@mantine/core'
import { useParams, Link } from '@tanstack/react-router'
import { useRunQuery } from '@/api/queries'
import { StateBadge } from '@/components/StateBadge'

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export function RunDetailPage() {
  const { id } = useParams({ from: '/auth/runs/$id' })
  const { data, isLoading, isError, error } = useRunQuery(id)

  if (isLoading) return <Loader size="sm" mt="xl" />
  if (isError)
    return (
      <Alert color="red" mt="xl">
        {error instanceof Error ? error.message : 'Failed to load run'}
      </Alert>
    )
  if (!data) return null

  const { run, files, transitions } = data

  return (
    <Box>
      <Group mb="xs" align="center">
        <Link to="/">
          <Anchor component="span" fz="sm" c="dimmed">Runs</Anchor>
        </Link>
        <Text c="dimmed" fz="sm">/</Text>
        <Text fz="sm" fw={500}>
          {run.id}
        </Text>
      </Group>

      <Group mb="md" align="center">
        <Title order={3}>{run.measurement_type}</Title>
        <StateBadge state={run.state} />
      </Group>

      {/* Metadata */}
      <Stack gap="xs" mb="xl">
        <Group>
          <Text fz="sm" c="dimmed" w={160}>
            Sample
          </Text>
          <Text fz="sm">{run.sample_id ?? '—'}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={160}>
            Device
          </Text>
          <Text fz="sm">{run.device_id ?? '—'}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={160}>
            Agent
          </Text>
          <Text fz="sm">{run.agent_id}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={160}>
            Declared at
          </Text>
          <Text fz="sm">{new Date(run.declared_at).toLocaleString()}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={160}>
            Declared by
          </Text>
          <Text fz="sm">{run.declared_by}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={160}>
            Operator comment
          </Text>
          <Text fz="sm">{run.operator_comment || '—'}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={160}>
            Manifest hash
          </Text>
          <Text ff="monospace" fz={12}>
            {run.manifest_hash}
          </Text>
        </Group>
      </Stack>

      <Divider mb="xl" />

      {/* Files */}
      <Title order={5} mb="sm">
        Files
      </Title>
      <Table withTableBorder mb="xl">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Name</Table.Th>
            <Table.Th>Size</Table.Th>
            <Table.Th>SHA-256</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {(!files || files.length === 0) && (
            <Table.Tr>
              <Table.Td colSpan={3}>
                <Text c="dimmed" fz="sm" ta="center" py="xs">
                  No files
                </Text>
              </Table.Td>
            </Table.Tr>
          )}
          {(files ?? []).map((f) => (
            <Table.Tr key={f.id}>
              <Table.Td fz="sm">{f.name}</Table.Td>
              <Table.Td fz="sm">{formatBytes(f.bytes)}</Table.Td>
              <Table.Td fz={12} ff="monospace" style={{ wordBreak: 'break-all' }}>
                {f.sha256.slice(0, 16)}…
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>

      {/* Audit trail */}
      <Title order={5} mb="sm">
        State history
      </Title>
      <Timeline active={(transitions?.length ?? 1) - 1} bulletSize={20} lineWidth={2}>
        {(transitions ?? []).map((t) => (
          <Timeline.Item
            key={t.id}
            title={
              <Text fz="sm" fw={500}>
                {t.from_state} → {t.to_state}
              </Text>
            }
          >
            <Text fz="xs" c="dimmed">
              {t.actor_type} · {t.actor_id}
              {t.reason ? ` · ${t.reason}` : ''}
            </Text>
            <Text fz="xs" c="dimmed">
              {new Date(t.created_at).toLocaleString()}
            </Text>
          </Timeline.Item>
        ))}
      </Timeline>
    </Box>
  )
}
