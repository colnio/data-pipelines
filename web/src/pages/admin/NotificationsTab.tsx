import { useEffect, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  PasswordInput,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  useNotificationConfigQuery,
  usePutNotificationConfigMutation,
  useTestNotificationMutation,
  useNotificationLogQuery,
} from '@/api/admin'
import { ApiError } from '@/api/client'
import { useIsAdmin } from '@/auth/useRole'
import type { NotifyConfigBody } from '@/api/admin'

const NOTIF_STATUS_COLOR: Record<string, string> = {
  sent: 'green',
  failed: 'red',
  skipped: 'gray',
  pending: 'yellow',
}

// ─── Config form state ────────────────────────────────────────────────────────

type ConfigFormState = {
  bot_token: string
  chat_id: string
  on_awaiting_review: boolean
  on_quarantined: boolean
  on_published: boolean
  on_test: boolean
}

function configToForm(cfg: NotifyConfigBody): ConfigFormState {
  return {
    bot_token: cfg.bot_token,
    chat_id: cfg.chat_id,
    on_awaiting_review: cfg.on_awaiting_review,
    on_quarantined: cfg.on_quarantined,
    on_published: cfg.on_published,
    on_test: cfg.on_test,
  }
}

const DEFAULT_FORM: ConfigFormState = {
  bot_token: '',
  chat_id: '',
  on_awaiting_review: false,
  on_quarantined: false,
  on_published: false,
  on_test: false,
}

// ─── Main tab ─────────────────────────────────────────────────────────────────

export function NotificationsTab() {
  const isAdmin = useIsAdmin()

  const { data: configData, isLoading: configLoading, isError: configError, error: configErr } =
    useNotificationConfigQuery()

  const putMutation = usePutNotificationConfigMutation()
  const testMutation = useTestNotificationMutation()

  const { data: logData, isLoading: logLoading, isError: logError, error: logErr } =
    useNotificationLogQuery(50)

  const [form, setForm] = useState<ConfigFormState>(DEFAULT_FORM)
  const [dirty, setDirty] = useState(false)

  // Sync form when config loads
  useEffect(() => {
    if (configData) {
      setForm(configToForm(configData))
      setDirty(false)
    }
  }, [configData])

  function updateField<K extends keyof ConfigFormState>(key: K, value: ConfigFormState[K]) {
    setForm((f) => ({ ...f, [key]: value }))
    setDirty(true)
  }

  function handleSave() {
    putMutation.mutate(form, {
      onSuccess: () => {
        notifications.show({ color: 'green', message: 'Notification config saved' })
        setDirty(false)
      },
      onError: (err) => {
        notifications.show({
          color: 'red',
          message: err instanceof ApiError ? err.message : 'Save failed',
        })
      },
    })
  }

  function handleTest() {
    testMutation.mutate(undefined, {
      onSuccess: () => {
        notifications.show({ color: 'blue', message: 'Test notification enqueued' })
      },
      onError: (err) => {
        notifications.show({
          color: 'red',
          message: err instanceof ApiError ? err.message : 'Test ping failed',
        })
      },
    })
  }

  const notifLog = logData?.notifications ?? []

  return (
    <Stack gap="xl">
      {/* Config form */}
      <Stack gap="md">
        <Title order={5}>Telegram configuration</Title>

        {configLoading && <Loader size="sm" />}
        {configError && (
          <Alert color="red">
            {configErr instanceof Error ? configErr.message : 'Failed to load config'}
          </Alert>
        )}

        {!configLoading && (
          <Stack gap="sm" maw={480}>
            <PasswordInput
              label="Bot token"
              description="Leave blank to use the server-side env var"
              value={form.bot_token}
              onChange={(e) => updateField('bot_token', e.currentTarget.value)}
              disabled={!isAdmin}
            />
            <TextInput
              label="Chat / channel ID"
              value={form.chat_id}
              onChange={(e) => updateField('chat_id', e.currentTarget.value)}
              disabled={!isAdmin}
            />

            <Stack gap="xs" mt="xs">
              <Text fz="sm" fw={500}>
                Notify on
              </Text>
              <Switch
                label="Run awaiting review"
                checked={form.on_awaiting_review}
                onChange={(e) => updateField('on_awaiting_review', e.currentTarget.checked)}
                disabled={!isAdmin}
              />
              <Switch
                label="Run quarantined"
                checked={form.on_quarantined}
                onChange={(e) => updateField('on_quarantined', e.currentTarget.checked)}
                disabled={!isAdmin}
              />
              <Switch
                label="Run published"
                checked={form.on_published}
                onChange={(e) => updateField('on_published', e.currentTarget.checked)}
                disabled={!isAdmin}
              />
              <Switch
                label="Test events"
                checked={form.on_test}
                onChange={(e) => updateField('on_test', e.currentTarget.checked)}
                disabled={!isAdmin}
              />
            </Stack>

            <Group mt="sm">
              <Tooltip label="admin only" disabled={isAdmin}>
                <Button
                  onClick={handleSave}
                  disabled={!isAdmin || !dirty}
                  loading={putMutation.isPending}
                >
                  Save config
                </Button>
              </Tooltip>
              <Tooltip label="admin only" disabled={isAdmin}>
                <Button
                  variant="light"
                  color="blue"
                  onClick={handleTest}
                  disabled={!isAdmin}
                  loading={testMutation.isPending}
                >
                  Send test ping
                </Button>
              </Tooltip>
            </Group>
          </Stack>
        )}
      </Stack>

      {/* Delivery log */}
      <Stack gap="md">
        <Title order={5}>Delivery log</Title>

        {logLoading && <Loader size="sm" />}
        {logError && (
          <Alert color="red">
            {logErr instanceof Error ? logErr.message : 'Failed to load log'}
          </Alert>
        )}

        {!logLoading && !logError && (
          <Table striped highlightOnHover withTableBorder>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Event</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Target</Table.Th>
                <Table.Th>Last error</Table.Th>
                <Table.Th>Sent at</Table.Th>
                <Table.Th>Created</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {notifLog.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={6}>
                    <Text c="dimmed" fz="sm" ta="center" py="md">
                      No notifications yet
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )}
              {notifLog.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Td fz="sm">{row.event_type}</Table.Td>
                  <Table.Td>
                    <Badge
                      color={NOTIF_STATUS_COLOR[row.status] ?? 'gray'}
                      variant="light"
                      size="sm"
                    >
                      {row.status}
                    </Badge>
                  </Table.Td>
                  <Table.Td fz="sm">{row.target || '—'}</Table.Td>
                  <Table.Td
                    fz="sm"
                    maw={240}
                    style={{
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {row.last_error || '—'}
                  </Table.Td>
                  <Table.Td fz="sm">
                    {row.sent_at ? new Date(row.sent_at).toLocaleString() : '—'}
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
