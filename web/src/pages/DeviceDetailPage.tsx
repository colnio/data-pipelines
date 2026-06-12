import { useState } from 'react'
import {
  Alert,
  Anchor,
  Box,
  Button,
  Divider,
  Group,
  Loader,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core'
import { useParams, Link } from '@tanstack/react-router'
import { useDeviceQuery, useContactConfigsQuery, type ContactConfig } from '@/api/catalog'
import { useRunsQuery, type ListRunsParams } from '@/api/queries'
import { StateBadge } from '@/components/StateBadge'

export function DeviceDetailPage() {
  const { id } = useParams({ from: '/auth/catalog/devices/$id' })

  // Contact-configs pagination
  const [ccCursor, setCcCursor] = useState<string | undefined>(undefined)
  const [allCcs, setAllCcs] = useState<ContactConfig[]>([])

  // Runs pagination
  const [runsCursor, setRunsCursor] = useState<string | undefined>(undefined)

  const { data: deviceData, isLoading: deviceLoading, isError: deviceError, error: deviceErr } =
    useDeviceQuery(id)

  const {
    data: ccData,
    isLoading: ccLoading,
    isError: ccError,
    error: ccErr,
  } = useContactConfigsQuery(id, ccCursor)

  const runsParams: ListRunsParams = { device_id: id, cursor: runsCursor }
  const {
    data: runsData,
    isLoading: runsLoading,
    isError: runsError,
    error: runsErr,
  } = useRunsQuery(runsParams)

  const freshCcs = ccData?.contact_configs ?? []
  const combinedCcs = ccCursor === undefined ? freshCcs : [...allCcs, ...freshCcs]

  const handleLoadMoreCcs = () => {
    if (ccData?.next_cursor) {
      setAllCcs(combinedCcs)
      setCcCursor(ccData.next_cursor)
    }
  }

  const handleResetCcs = () => {
    setAllCcs([])
    setCcCursor(undefined)
  }

  const contactConfigs = combinedCcs
  const runs = runsData?.runs ?? []

  return (
    <Box>
      {/* Breadcrumb */}
      <Group mb="xs">
        <Link to="/catalog">
          <Anchor component="span" fz="sm" c="dimmed">
            Catalog
          </Anchor>
        </Link>
        <Text c="dimmed" fz="sm">/</Text>
        <Text fz="sm" fw={500}>
          Device {id.slice(0, 8)}…
        </Text>
      </Group>

      {/* Device header */}
      {deviceLoading && <Loader size="sm" />}
      {deviceError && (
        <Alert color="red" mb="md">
          {deviceErr instanceof Error ? deviceErr.message : 'Failed to load device'}
        </Alert>
      )}
      {deviceData && (
        <>
          <Title order={3} mb="sm">
            Device
          </Title>
          <Stack gap="xs" mb="lg">
            <Group>
              <Text fz="sm" c="dimmed" w={180}>ID</Text>
              <Text fz="sm" ff="monospace">{deviceData.device.id}</Text>
            </Group>
            <Group>
              <Text fz="sm" c="dimmed" w={180}>Class</Text>
              <Text fz="sm">{deviceData.device.device_class}</Text>
            </Group>
            <Group>
              <Text fz="sm" c="dimmed" w={180}>Fabrication ID</Text>
              <Text fz="sm">{deviceData.device.fabrication_id ?? '—'}</Text>
            </Group>
            <Group>
              <Text fz="sm" c="dimmed" w={180}>Lifecycle state</Text>
              <Text fz="sm">{deviceData.device.lifecycle_state ?? '—'}</Text>
            </Group>
            {deviceData.device.sample_id && (
              <Group>
                <Text fz="sm" c="dimmed" w={180}>Sample</Text>
                <Link to="/catalog/samples/$id" params={{ id: deviceData.device.sample_id }}>
                  <Anchor component="span" fz="sm" ff="monospace">
                    {deviceData.device.sample_id.slice(0, 8)}…
                  </Anchor>
                </Link>
              </Group>
            )}
            <Group>
              <Text fz="sm" c="dimmed" w={180}>Created</Text>
              <Text fz="sm">{new Date(deviceData.device.created_at).toLocaleString()}</Text>
            </Group>
            {deviceData.device.notes && (
              <Group>
                <Text fz="sm" c="dimmed" w={180}>Notes</Text>
                <Text fz="sm">{deviceData.device.notes}</Text>
              </Group>
            )}
          </Stack>
        </>
      )}

      <Divider mb="lg" />

      {/* Contact configs */}
      <Title order={5} mb="sm">
        Contact configurations
      </Title>

      {ccLoading && <Loader size="sm" />}
      {ccError && (
        <Alert color="red" mb="md">
          {ccErr instanceof Error ? ccErr.message : 'Failed to load contact configs'}
        </Alert>
      )}

      {!ccLoading && !ccError && (
        <>
          <Table withTableBorder mb="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>ID</Table.Th>
                <Table.Th>Default</Table.Th>
                <Table.Th>Created</Table.Th>
                <Table.Th>Notes</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {contactConfigs.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={4}>
                    <Text c="dimmed" fz="sm" ta="center" py="md">
                      No contact configs
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )}
              {contactConfigs.map((cc) => (
                <Table.Tr key={cc.id}>
                  <Table.Td fz="sm" ff="monospace">{cc.id.slice(0, 8)}…</Table.Td>
                  <Table.Td fz="sm">{cc.is_default ? 'Yes' : 'No'}</Table.Td>
                  <Table.Td fz="sm">{new Date(cc.created_at).toLocaleDateString()}</Table.Td>
                  <Table.Td fz="sm">{cc.notes ?? '—'}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          <Group mb="lg">
            {ccData?.next_cursor && (
              <Button variant="subtle" size="xs" onClick={handleLoadMoreCcs} loading={ccLoading}>
                Load more
              </Button>
            )}
            {ccCursor !== undefined && (
              <Button variant="subtle" size="xs" color="gray" onClick={handleResetCcs}>
                Reset
              </Button>
            )}
          </Group>
        </>
      )}

      <Divider mb="lg" />

      {/* Runs for this device */}
      <Title order={5} mb="sm">
        Runs
      </Title>

      {runsLoading && <Loader size="sm" />}
      {runsError && (
        <Alert color="red" mb="md">
          {runsErr instanceof Error ? runsErr.message : 'Failed to load runs'}
        </Alert>
      )}

      {!runsLoading && !runsError && (
        <>
          <Table striped highlightOnHover withTableBorder>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Run ID</Table.Th>
                <Table.Th>State</Table.Th>
                <Table.Th>Measurement type</Table.Th>
                <Table.Th>Declared at</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {runs.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={4}>
                    <Text c="dimmed" fz="sm" ta="center" py="md">
                      No runs
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )}
              {runs.map((r) => (
                <Table.Tr key={r.id}>
                  <Table.Td>
                    <Link to="/runs/$id" params={{ id: r.id }}>
                      <Anchor component="span" fz="sm" ff="monospace">
                        {r.id.slice(0, 8)}…
                      </Anchor>
                    </Link>
                  </Table.Td>
                  <Table.Td>
                    <StateBadge state={r.state} />
                  </Table.Td>
                  <Table.Td fz="sm">{r.measurement_type}</Table.Td>
                  <Table.Td fz="sm">{new Date(r.declared_at).toLocaleString()}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>

          <Group mt="md">
            {runsData?.next_cursor && (
              <Button
                variant="subtle"
                size="xs"
                onClick={() => setRunsCursor(runsData.next_cursor)}
                loading={runsLoading}
              >
                Next
              </Button>
            )}
            {runsCursor !== undefined && (
              <Button
                variant="subtle"
                size="xs"
                color="gray"
                onClick={() => setRunsCursor(undefined)}
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
