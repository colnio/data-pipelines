import { useState } from 'react'
import {
  Alert,
  Anchor,
  Box,
  Button,
  Code,
  Divider,
  Group,
  Loader,
  Modal,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Timeline,
  Title,
} from '@mantine/core'
import { useParams, Link, useNavigate } from '@tanstack/react-router'
import { notifications } from '@mantine/notifications'
import {
  useReviewQuery,
  useApproveMutation,
  useRequestChangesMutation,
  useQuarantineMutation,
  usePatchMetadataMutation,
} from '@/api/queries'
import { StateBadge } from '@/components/StateBadge'

function MetricsTable({ metrics }: { metrics: unknown }) {
  if (!metrics || typeof metrics !== 'object') return <Text fz="sm" c="dimmed">No metrics</Text>
  const entries = Object.entries(metrics as Record<string, unknown>)
  if (entries.length === 0) return <Text fz="sm" c="dimmed">No metrics</Text>
  return (
    <Table withTableBorder fz="sm">
      <Table.Thead>
        <Table.Tr>
          <Table.Th>Metric</Table.Th>
          <Table.Th>Value</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {entries.map(([k, v]) => (
          <Table.Tr key={k}>
            <Table.Td>{k}</Table.Td>
            <Table.Td ff="monospace">{String(v)}</Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  )
}

function ParserWarnings({ warnings }: { warnings: unknown }) {
  if (!warnings) return <Text fz="sm" c="dimmed">None</Text>
  const list = Array.isArray(warnings) ? warnings : [warnings]
  if (list.length === 0) return <Text fz="sm" c="dimmed">None</Text>
  return (
    <Stack gap={4}>
      {list.map((w, i) => (
        <Text key={i} fz="sm" c="orange">
          {String(w)}
        </Text>
      ))}
    </Stack>
  )
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export function ReviewDetailPage() {
  const { runId } = useParams({ from: '/auth/reviews/$runId' })
  const navigate = useNavigate()

  const { data, isLoading, isError, error } = useReviewQuery(runId)

  const approveMutation = useApproveMutation()
  const requestChangesMutation = useRequestChangesMutation()
  const quarantineMutation = useQuarantineMutation()
  const patchMetadataMutation = usePatchMetadataMutation()

  const [changesModal, setChangesModal] = useState(false)
  const [quarantineModal, setQuarantineModal] = useState(false)
  const [approveModal, setApproveModal] = useState(false)
  const [reason, setReason] = useState('')
  const [operatorComment, setOperatorComment] = useState('')

  if (isLoading) return <Loader size="sm" mt="xl" />
  if (isError)
    return (
      <Alert color="red" mt="xl">
        {error instanceof Error ? error.message : 'Failed to load review'}
      </Alert>
    )
  if (!data) return null

  const { run, files, transitions, latest_artifact } = data

  const afterAction = () => {
    void navigate({ to: '/reviews' })
  }

  const handleApprove = async () => {
    try {
      await approveMutation.mutateAsync(runId)
      notifications.show({ message: 'Run approved', color: 'green' })
      afterAction()
    } catch (e) {
      notifications.show({ message: String(e), color: 'red' })
    }
    setApproveModal(false)
  }

  const handleRequestChanges = async () => {
    try {
      await requestChangesMutation.mutateAsync({ runId, reason })
      notifications.show({ message: 'Changes requested', color: 'yellow' })
      afterAction()
    } catch (e) {
      notifications.show({ message: String(e), color: 'red' })
    }
    setChangesModal(false)
    setReason('')
  }

  const handleQuarantine = async () => {
    try {
      await quarantineMutation.mutateAsync({ runId, reason })
      notifications.show({ message: 'Run quarantined', color: 'orange' })
      afterAction()
    } catch (e) {
      notifications.show({ message: String(e), color: 'red' })
    }
    setQuarantineModal(false)
    setReason('')
  }

  const handlePatchMetadata = async () => {
    try {
      await patchMetadataMutation.mutateAsync({ runId, operator_comment: operatorComment })
      notifications.show({ message: 'Comment updated', color: 'blue' })
    } catch (e) {
      notifications.show({ message: String(e), color: 'red' })
    }
  }

  return (
    <Box>
      {/* Breadcrumb */}
      <Group mb="xs">
        <Link to="/reviews">
          <Anchor component="span" fz="sm" c="dimmed">Reviews</Anchor>
        </Link>
        <Text c="dimmed" fz="sm">
          /
        </Text>
        <Text fz="sm" fw={500}>
          {run.id}
        </Text>
      </Group>

      <Group mb="md" align="center">
        <Title order={3}>{run.measurement_type}</Title>
        <StateBadge state={run.state} />
      </Group>

      {/* Run metadata */}
      <Stack gap="xs" mb="lg">
        <Group>
          <Text fz="sm" c="dimmed" w={180}>
            Sample
          </Text>
          <Text fz="sm">{run.sample_id ?? '—'}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={180}>
            Device
          </Text>
          <Text fz="sm">{run.device_id ?? '—'}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={180}>
            Agent
          </Text>
          <Text fz="sm">{run.agent_id}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={180}>
            Declared at
          </Text>
          <Text fz="sm">{new Date(run.declared_at).toLocaleString()}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={180}>
            Declared by
          </Text>
          <Text fz="sm">{run.declared_by}</Text>
        </Group>
        <Group>
          <Text fz="sm" c="dimmed" w={180}>
            Operator comment
          </Text>
          <Text fz="sm">{run.operator_comment || '—'}</Text>
        </Group>
      </Stack>

      <Divider mb="lg" />

      {/* Review artifact */}
      {latest_artifact && (
        <>
          <Title order={5} mb="sm">
            Metrics (v{latest_artifact.version})
          </Title>
          <Box mb="lg">
            <MetricsTable metrics={latest_artifact.metrics_json} />
          </Box>

          <Title order={5} mb="sm">
            Parser warnings
          </Title>
          <Box mb="lg">
            <ParserWarnings warnings={latest_artifact.parser_warnings_json} />
          </Box>

          {latest_artifact.llm_summary && (
            <>
              <Title order={5} mb="sm">
                LLM summary
              </Title>
              <Text fz="sm" mb="lg" style={{ whiteSpace: 'pre-wrap' }}>
                {latest_artifact.llm_summary}
              </Text>
            </>
          )}

          {latest_artifact.plots_json && (
            <>
              <Title order={5} mb="sm">
                Plot references
              </Title>
              <Text fz="xs" c="dimmed" mb="xs">
                plots_json contains server-side file paths. Image serving is a
                follow-up endpoint — display only.
              </Text>
              <Code block mb="lg" fz={12}>
                {JSON.stringify(latest_artifact.plots_json, null, 2)}
              </Code>
            </>
          )}

          <Divider mb="lg" />
        </>
      )}

      {/* Files */}
      <Title order={5} mb="sm">
        Files
      </Title>
      <Table withTableBorder mb="lg">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Name</Table.Th>
            <Table.Th>Size</Table.Th>
            <Table.Th>SHA-256 (prefix)</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {(!files || files.length === 0) && (
            <Table.Tr>
              <Table.Td colSpan={3}>
                <Text c="dimmed" fz="sm" ta="center">
                  No files
                </Text>
              </Table.Td>
            </Table.Tr>
          )}
          {(files ?? []).map((f) => (
            <Table.Tr key={f.id}>
              <Table.Td fz="sm">{f.name}</Table.Td>
              <Table.Td fz="sm">{formatBytes(f.bytes)}</Table.Td>
              <Table.Td ff="monospace" fz={12}>
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
      <Box mb="xl">
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

      <Divider mb="lg" />

      {/* Edit operator comment */}
      <Title order={5} mb="sm">
        Update operator comment
      </Title>
      <Group mb="xl" align="flex-end">
        <TextInput
          value={operatorComment}
          onChange={(e) => setOperatorComment(e.currentTarget.value)}
          placeholder={run.operator_comment || 'Enter comment…'}
          w={340}
        />
        <Button
          variant="outline"
          onClick={handlePatchMetadata}
          loading={patchMetadataMutation.isPending}
        >
          Save
        </Button>
      </Group>

      {/* Action buttons */}
      <Title order={5} mb="sm">
        Review actions
      </Title>
      <Group>
        <Button color="green" onClick={() => setApproveModal(true)}>
          Approve
        </Button>
        <Button color="orange" variant="outline" onClick={() => setChangesModal(true)}>
          Request changes
        </Button>
        <Button color="red" variant="outline" onClick={() => setQuarantineModal(true)}>
          Quarantine
        </Button>
      </Group>

      {/* Approve confirm modal */}
      <Modal
        opened={approveModal}
        onClose={() => setApproveModal(false)}
        title="Approve run?"
        size="sm"
      >
        <Text fz="sm" mb="lg">
          This will transition the run to <strong>approved</strong> and enqueue publishing.
        </Text>
        <Group justify="flex-end">
          <Button variant="subtle" onClick={() => setApproveModal(false)}>
            Cancel
          </Button>
          <Button color="green" onClick={handleApprove} loading={approveMutation.isPending}>
            Confirm approve
          </Button>
        </Group>
      </Modal>

      {/* Request changes modal */}
      <Modal
        opened={changesModal}
        onClose={() => { setChangesModal(false); setReason('') }}
        title="Request changes"
        size="sm"
      >
        <Textarea
          label="Reason"
          value={reason}
          onChange={(e) => setReason(e.currentTarget.value)}
          placeholder="Describe what needs to be changed…"
          mb="lg"
          minRows={3}
        />
        <Group justify="flex-end">
          <Button variant="subtle" onClick={() => { setChangesModal(false); setReason('') }}>
            Cancel
          </Button>
          <Button
            color="orange"
            onClick={handleRequestChanges}
            loading={requestChangesMutation.isPending}
            disabled={!reason.trim()}
          >
            Submit
          </Button>
        </Group>
      </Modal>

      {/* Quarantine modal */}
      <Modal
        opened={quarantineModal}
        onClose={() => { setQuarantineModal(false); setReason('') }}
        title="Quarantine run"
        size="sm"
      >
        <Textarea
          label="Reason"
          value={reason}
          onChange={(e) => setReason(e.currentTarget.value)}
          placeholder="Describe why this run is being quarantined…"
          mb="lg"
          minRows={3}
        />
        <Group justify="flex-end">
          <Button variant="subtle" onClick={() => { setQuarantineModal(false); setReason('') }}>
            Cancel
          </Button>
          <Button
            color="red"
            onClick={handleQuarantine}
            loading={quarantineMutation.isPending}
            disabled={!reason.trim()}
          >
            Quarantine
          </Button>
        </Group>
      </Modal>
    </Box>
  )
}
