import {
  Alert,
  Badge,
  Box,
  Button,
  Card,
  CopyButton,
  Divider,
  Group,
  Loader,
  Stack,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { ApiError } from '@/api/client'
import {
  useJupyterInfoQuery,
  useJupyterConnectionMutation,
  type JupyterConnection,
} from '@/api/jupyter'

// ─── Helpers ──────────────────────────────────────────────────────────────────

function fmtDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString(undefined, {
      dateStyle: 'medium',
      timeStyle: 'short',
    })
  } catch {
    return iso
  }
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function CopyableField({
  label,
  value,
}: {
  label: string
  value: string
}) {
  return (
    <Box>
      <Text fz="xs" c="dimmed" mb={4}>
        {label}
      </Text>
      <Group gap="xs" wrap="nowrap" align="center">
        <TextInput
          value={value}
          readOnly
          styles={{ input: { fontFamily: 'monospace', fontSize: 12 } }}
          style={{ flex: 1 }}
        />
        <CopyButton value={value} timeout={2000}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? 'Copied!' : 'Copy'} withArrow>
              <Button
                size="xs"
                variant={copied ? 'filled' : 'default'}
                color={copied ? 'teal' : undefined}
                onClick={() => {
                  copy()
                  notifications.show({
                    message: `${label} copied`,
                    color: 'teal',
                    autoClose: 1500,
                  })
                }}
              >
                {copied ? 'Copied' : 'Copy'}
              </Button>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
    </Box>
  )
}

function ConnectionDetails({ conn }: { conn: JupyterConnection }) {
  return (
    <Card withBorder mt="md" p="md">
      <Title order={5} mb="sm">
        Connection details
      </Title>
      <Stack gap="sm">
        <CopyableField label="VS Code server URI (paste this)" value={conn.vscode_server_uri} />
        <CopyableField label="Token" value={conn.token} />

        <Divider my={4} />

        <Group gap="xl" wrap="wrap">
          <Box>
            <Text fz="xs" c="dimmed">
              Server URL
            </Text>
            <Text fz="sm" style={{ fontFamily: 'monospace' }}>
              {conn.server_url}
            </Text>
          </Box>
          <Box>
            <Text fz="xs" c="dimmed">
              Username
            </Text>
            <Text fz="sm">{conn.username}</Text>
          </Box>
          <Box>
            <Text fz="xs" c="dimmed">
              Expires at
            </Text>
            <Text fz="sm">{fmtDate(conn.expires_at)}</Text>
          </Box>
        </Group>

        <Divider my={4} />

        <Box>
          <Text fz="sm" fw={500} mb={4}>
            How to connect from VS Code
          </Text>
          <Stack gap={2}>
            {[
              '1. Open the Command Palette (Cmd/Ctrl + Shift + P).',
              '2. Run "Jupyter: Specify Jupyter Server for Connections".',
              '3. Choose "Existing: Specify the URI of an existing server".',
              '4. Paste the VS Code server URI above and press Enter.',
            ].map((step) => (
              <Text key={step} fz="sm" c="dimmed">
                {step}
              </Text>
            ))}
          </Stack>
        </Box>
      </Stack>
    </Card>
  )
}

// ─── Page ─────────────────────────────────────────────────────────────────────

export function JupyterPage() {
  const infoQuery = useJupyterInfoQuery()
  const connMutation = useJupyterConnectionMutation()

  const info = infoQuery.data

  const handleGenerateLink = () => {
    connMutation.mutate(undefined, {
      onSuccess: () => {
        notifications.show({
          message: 'Connection link generated',
          color: 'teal',
          autoClose: 2500,
        })
      },
    })
  }

  // Derive a friendly error message for the connection mutation
  let connErrorMsg: string | null = null
  if (connMutation.isError) {
    const err = connMutation.error
    if (err instanceof ApiError && err.code === 'jupyter.not_configured') {
      connErrorMsg =
        'JupyterHub is not configured on the server yet (set JUPYTERHUB_URL / JUPYTERHUB_ADMIN_TOKEN).'
    } else if (err instanceof Error) {
      connErrorMsg = err.message
    } else {
      connErrorMsg = 'An unexpected error occurred. Please try again.'
    }
  }

  return (
    <Box maw={760}>
      <Title order={3}>Jupyter</Title>
      <Text c="dimmed" fz="sm" mt={4} mb="xl">
        Connect to JupyterHub from your browser or IDE.
      </Text>

      {/* ── Info card ─────────────────────────────────────────────────────── */}
      <Card withBorder mb="xl" p="md">
        <Title order={4} mb="sm">
          Hub status
        </Title>

        {infoQuery.isLoading && <Loader size="sm" />}

        {infoQuery.isError && (
          <Alert color="red">
            {infoQuery.error instanceof Error
              ? infoQuery.error.message
              : 'Failed to load JupyterHub info'}
          </Alert>
        )}

        {info && (
          <Stack gap="sm">
            <Group gap="md" wrap="wrap">
              <Box>
                <Text fz="xs" c="dimmed">
                  Hub URL
                </Text>
                <Text fz="sm" style={{ fontFamily: 'monospace' }}>
                  {info.hub_url}
                </Text>
              </Box>
              <Box>
                <Text fz="xs" c="dimmed" mb={2}>
                  Status
                </Text>
                <Badge color={info.reachable ? 'green' : 'red'} variant="filled">
                  {info.reachable ? 'up' : 'unreachable'}
                </Badge>
              </Box>
              <Box>
                <Text fz="xs" c="dimmed">
                  Checked at
                </Text>
                <Text fz="sm">{fmtDate(info.checked_at)}</Text>
              </Box>
            </Group>

            <Button
              variant="light"
              size="xs"
              w="fit-content"
              onClick={() => window.open(info.hub_url, '_blank')}
            >
              Open in browser
            </Button>
          </Stack>
        )}
      </Card>

      {/* ── IDE connection section ────────────────────────────────────────── */}
      <Card withBorder p="md">
        <Title order={4} mb={4}>
          Connect from your IDE (VS Code)
        </Title>
        <Text fz="sm" c="dimmed" mb="md">
          Generate a short-lived connection link to attach VS Code's Jupyter extension to your
          personal server.
        </Text>

        <Button
          onClick={handleGenerateLink}
          loading={connMutation.isPending}
          disabled={connMutation.isPending}
        >
          Generate connection link
        </Button>

        {connErrorMsg && (
          <Alert color="red" mt="md">
            {connErrorMsg}
          </Alert>
        )}

        {connMutation.data && <ConnectionDetails conn={connMutation.data} />}
      </Card>
    </Box>
  )
}
