import { useState } from 'react'
import {
  Alert,
  Badge,
  Box,
  Button,
  Code,
  CopyButton,
  Group,
  Loader,
  Modal,
  Stack,
  Table,
  Text,
  TextInput,
  Textarea,
  Title,
  Tooltip,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import {
  useAdminAgentsQuery,
  useRegisterAgentMutation,
  useRotateAgentKeyMutation,
  usePatchAgentMutation,
} from '@/api/admin'
import { ApiError } from '@/api/client'
import { useIsAdmin } from '@/auth/useRole'

// ─── One-time key display ─────────────────────────────────────────────────────

function RawKeyDisplay({ rawKey, onClose }: { rawKey: string; onClose: () => void }) {
  return (
    <Stack gap="sm">
      <Alert color="orange" title="Save this key now">
        This is the only time the key will be shown. Copy it and store it securely.
      </Alert>
      <Code block fz="sm" style={{ wordBreak: 'break-all', userSelect: 'all' }}>
        {rawKey}
      </Code>
      <Group>
        <CopyButton value={rawKey}>
          {({ copied, copy }) => (
            <Button color={copied ? 'green' : 'blue'} onClick={copy} variant="light">
              {copied ? 'Copied!' : 'Copy key'}
            </Button>
          )}
        </CopyButton>
        <Button variant="subtle" color="gray" onClick={onClose}>
          I have saved it — close
        </Button>
      </Group>
    </Stack>
  )
}

// ─── Register agent modal ─────────────────────────────────────────────────────

interface RegisterModalProps {
  opened: boolean
  onClose: () => void
}

function RegisterAgentModal({ opened, onClose }: RegisterModalProps) {
  const [agentId, setAgentId] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [rootsText, setRootsText] = useState('')
  const [rawKey, setRawKey] = useState<string | null>(null)

  const registerMutation = useRegisterAgentMutation()

  function handleSubmit() {
    const allowed_roots = rootsText
      .split(/[\n,]+/)
      .map((s) => s.trim())
      .filter(Boolean)

    registerMutation.mutate(
      { id: agentId, display_name: displayName, allowed_roots },
      {
        onSuccess: (data) => {
          setRawKey(data.raw_key)
          notifications.show({ color: 'green', message: 'Agent registered' })
        },
        onError: (err) => {
          notifications.show({
            color: 'red',
            message: err instanceof ApiError ? err.message : 'Registration failed',
          })
        },
      },
    )
  }

  function handleClose() {
    setAgentId('')
    setDisplayName('')
    setRootsText('')
    setRawKey(null)
    onClose()
  }

  return (
    <Modal opened={opened} onClose={handleClose} title="Register agent" size="md">
      {rawKey ? (
        <RawKeyDisplay rawKey={rawKey} onClose={handleClose} />
      ) : (
        <Stack gap="sm">
          <TextInput
            label="Agent ID"
            description="Human-meaningful identifier (e.g. lab-workstation-1)"
            value={agentId}
            onChange={(e) => setAgentId(e.currentTarget.value)}
            required
          />
          <TextInput
            label="Display name"
            value={displayName}
            onChange={(e) => setDisplayName(e.currentTarget.value)}
            required
          />
          <Textarea
            label="Allowed roots"
            description="Filesystem roots the agent may expose — one per line or comma-separated"
            value={rootsText}
            onChange={(e) => setRootsText(e.currentTarget.value)}
            minRows={3}
          />
          <Group justify="flex-end" mt="xs">
            <Button variant="subtle" color="gray" onClick={handleClose}>
              Cancel
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={!agentId || !displayName}
              loading={registerMutation.isPending}
            >
              Register
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}

// ─── Rotate key modal ─────────────────────────────────────────────────────────

interface RotateModalProps {
  agentId: string | null
  onClose: () => void
}

function RotateKeyModal({ agentId, onClose }: RotateModalProps) {
  const [rawKey, setRawKey] = useState<string | null>(null)
  const rotateMutation = useRotateAgentKeyMutation()

  function handleRotate() {
    if (!agentId) return
    rotateMutation.mutate(agentId, {
      onSuccess: (data) => {
        setRawKey(data.raw_key)
        notifications.show({ color: 'green', message: 'Key rotated' })
      },
      onError: (err) => {
        notifications.show({
          color: 'red',
          message: err instanceof ApiError ? err.message : 'Rotation failed',
        })
      },
    })
  }

  function handleClose() {
    setRawKey(null)
    onClose()
  }

  return (
    <Modal
      opened={!!agentId}
      onClose={handleClose}
      title={`Rotate key — ${agentId ?? ''}`}
      size="md"
    >
      {rawKey ? (
        <RawKeyDisplay rawKey={rawKey} onClose={handleClose} />
      ) : (
        <Stack gap="sm">
          <Alert color="orange" title="Warning">
            Rotating the key will immediately invalidate the existing key. Any running agent
            using the old key will be disconnected.
          </Alert>
          <Group justify="flex-end">
            <Button variant="subtle" color="gray" onClick={handleClose}>
              Cancel
            </Button>
            <Button color="red" onClick={handleRotate} loading={rotateMutation.isPending}>
              Rotate key
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}

// ─── Edit allowed roots modal ─────────────────────────────────────────────────

interface EditRootsModalProps {
  agentId: string | null
  currentRoots: string[]
  onClose: () => void
}

function EditRootsModal({ agentId, currentRoots, onClose }: EditRootsModalProps) {
  const [rootsText, setRootsText] = useState(currentRoots.join('\n'))
  const patchMutation = usePatchAgentMutation()

  function handleSave() {
    if (!agentId) return
    const allowed_roots = rootsText
      .split(/[\n,]+/)
      .map((s) => s.trim())
      .filter(Boolean)
    patchMutation.mutate(
      { id: agentId, body: { allowed_roots } },
      {
        onSuccess: () => {
          notifications.show({ color: 'green', message: 'Allowed roots updated' })
          onClose()
        },
        onError: (err) => {
          notifications.show({
            color: 'red',
            message: err instanceof ApiError ? err.message : 'Update failed',
          })
        },
      },
    )
  }

  return (
    <Modal
      opened={!!agentId}
      onClose={onClose}
      title={`Edit allowed roots — ${agentId ?? ''}`}
      size="md"
    >
      <Stack gap="sm">
        <Textarea
          label="Allowed roots"
          description="One path per line or comma-separated"
          value={rootsText}
          onChange={(e) => setRootsText(e.currentTarget.value)}
          minRows={4}
          autosize
        />
        <Group justify="flex-end">
          <Button variant="subtle" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} loading={patchMutation.isPending}>
            Save
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

// ─── Main tab ─────────────────────────────────────────────────────────────────

export function AgentsTab() {
  const isAdmin = useIsAdmin()
  const [registerOpened, { open: openRegister, close: closeRegister }] = useDisclosure(false)
  const [rotateAgentId, setRotateAgentId] = useState<string | null>(null)
  const [editRootsAgent, setEditRootsAgent] = useState<{
    id: string
    roots: string[]
  } | null>(null)

  const { data, isLoading, isError, error } = useAdminAgentsQuery()
  const patchMutation = usePatchAgentMutation()

  const agents = data?.agents ?? []

  function handleToggle(id: string, enabled: boolean, name: string) {
    patchMutation.mutate(
      { id, body: { enabled: !enabled } },
      {
        onSuccess: () => {
          notifications.show({
            color: 'green',
            message: `Agent ${name} ${enabled ? 'disabled' : 'enabled'}`,
          })
        },
        onError: (err) => {
          notifications.show({
            color: 'red',
            message: err instanceof ApiError ? err.message : 'Update failed',
          })
        },
      },
    )
  }

  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Title order={5}>Agents</Title>
        <Tooltip label="admin only" disabled={isAdmin}>
          <Button
            size="sm"
            onClick={openRegister}
            disabled={!isAdmin}
          >
            Register agent
          </Button>
        </Tooltip>
      </Group>

      {isLoading && <Loader size="sm" />}
      {isError && (
        <Alert color="red">
          {error instanceof Error ? error.message : 'Failed to load agents'}
        </Alert>
      )}

      {!isLoading && !isError && (
        <Table striped highlightOnHover withTableBorder>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>ID</Table.Th>
              <Table.Th>Display name</Table.Th>
              <Table.Th>Status</Table.Th>
              <Table.Th>Allowed roots</Table.Th>
              <Table.Th>Last seen</Table.Th>
              <Table.Th>Actions</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {agents.length === 0 && (
              <Table.Tr>
                <Table.Td colSpan={6}>
                  <Text c="dimmed" fz="sm" ta="center" py="md">
                    No agents registered
                  </Text>
                </Table.Td>
              </Table.Tr>
            )}
            {agents.map((agent) => (
              <Table.Tr key={agent.id}>
                <Table.Td fz="sm">
                  <Code>{agent.id}</Code>
                </Table.Td>
                <Table.Td fz="sm">{agent.display_name}</Table.Td>
                <Table.Td>
                  <Badge
                    color={agent.enabled ? 'green' : 'gray'}
                    variant="light"
                    size="sm"
                  >
                    {agent.enabled ? 'enabled' : 'disabled'}
                  </Badge>
                </Table.Td>
                <Table.Td fz="sm">
                  <Box maw={240} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {(agent.allowed_roots ?? []).join(', ') || '—'}
                  </Box>
                </Table.Td>
                <Table.Td fz="sm">
                  {agent.last_seen_at
                    ? new Date(agent.last_seen_at).toLocaleString()
                    : '—'}
                </Table.Td>
                <Table.Td>
                  <Group gap="xs">
                    <Tooltip label={isAdmin ? undefined : 'admin only'} disabled={isAdmin}>
                      <Button
                        size="xs"
                        variant="light"
                        color={agent.enabled ? 'gray' : 'green'}
                        disabled={!isAdmin || patchMutation.isPending}
                        onClick={() =>
                          handleToggle(agent.id, agent.enabled, agent.display_name)
                        }
                      >
                        {agent.enabled ? 'Disable' : 'Enable'}
                      </Button>
                    </Tooltip>
                    <Tooltip label={isAdmin ? undefined : 'admin only'} disabled={isAdmin}>
                      <Button
                        size="xs"
                        variant="light"
                        color="orange"
                        disabled={!isAdmin}
                        onClick={() => setRotateAgentId(agent.id)}
                      >
                        Rotate key
                      </Button>
                    </Tooltip>
                    <Tooltip label={isAdmin ? undefined : 'admin only'} disabled={isAdmin}>
                      <Button
                        size="xs"
                        variant="light"
                        disabled={!isAdmin}
                        onClick={() =>
                          setEditRootsAgent({ id: agent.id, roots: agent.allowed_roots ?? [] })
                        }
                      >
                        Edit roots
                      </Button>
                    </Tooltip>
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}

      <RegisterAgentModal opened={registerOpened} onClose={closeRegister} />
      <RotateKeyModal agentId={rotateAgentId} onClose={() => setRotateAgentId(null)} />
      {editRootsAgent && (
        <EditRootsModal
          agentId={editRootsAgent.id}
          currentRoots={editRootsAgent.roots}
          onClose={() => setEditRootsAgent(null)}
        />
      )}
    </Stack>
  )
}
