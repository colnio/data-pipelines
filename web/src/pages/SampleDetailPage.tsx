import { useState } from 'react'
import { Alert, Anchor, Box, Button, Divider, Group, Loader, Stack, Table, Text, Title } from '@mantine/core'
import { useParams, Link } from '@tanstack/react-router'
import { useSampleQuery, useDevicesQuery, type Device } from '@/api/catalog'

export function SampleDetailPage() {
  const { id } = useParams({ from: '/auth/catalog/samples/$id' })
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [allDevices, setAllDevices] = useState<Device[]>([])

  const { data: sampleData, isLoading: sampleLoading, isError: sampleError, error: sampleErr } =
    useSampleQuery(id)

  const {
    data: devicesData,
    isLoading: devicesLoading,
    isError: devicesError,
    error: devicesErr,
  } = useDevicesQuery(id, cursor)

  const freshDevices = devicesData?.devices ?? []
  const combinedDevices = cursor === undefined
    ? freshDevices
    : [...allDevices, ...freshDevices]

  const handleLoadMore = () => {
    if (devicesData?.next_cursor) {
      setAllDevices(combinedDevices)
      setCursor(devicesData.next_cursor)
    }
  }

  const handleReset = () => {
    setAllDevices([])
    setCursor(undefined)
  }

  const devices = combinedDevices

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
          Sample {id.slice(0, 8)}…
        </Text>
      </Group>

      {/* Sample header */}
      {sampleLoading && <Loader size="sm" />}
      {sampleError && (
        <Alert color="red" mb="md">
          {sampleErr instanceof Error ? sampleErr.message : 'Failed to load sample'}
        </Alert>
      )}
      {sampleData && (
        <>
          <Title order={3} mb="sm">
            Sample
          </Title>
          <Stack gap="xs" mb="lg">
            <Group>
              <Text fz="sm" c="dimmed" w={180}>ID</Text>
              <Text fz="sm" ff="monospace">{sampleData.sample.id}</Text>
            </Group>
            <Group>
              <Text fz="sm" c="dimmed" w={180}>Material stack</Text>
              <Text fz="sm">{sampleData.sample.material_stack ?? '—'}</Text>
            </Group>
            <Group>
              <Text fz="sm" c="dimmed" w={180}>Fabrication batch</Text>
              <Text fz="sm">{sampleData.sample.fabrication_batch ?? '—'}</Text>
            </Group>
            <Group>
              <Text fz="sm" c="dimmed" w={180}>Created</Text>
              <Text fz="sm">{new Date(sampleData.sample.created_at).toLocaleString()}</Text>
            </Group>
            {sampleData.sample.notes && (
              <Group>
                <Text fz="sm" c="dimmed" w={180}>Notes</Text>
                <Text fz="sm">{sampleData.sample.notes}</Text>
              </Group>
            )}
          </Stack>
        </>
      )}

      <Divider mb="lg" />

      {/* Devices */}
      <Title order={5} mb="sm">
        Devices
      </Title>

      {devicesLoading && <Loader size="sm" />}
      {devicesError && (
        <Alert color="red" mb="md">
          {devicesErr instanceof Error ? devicesErr.message : 'Failed to load devices'}
        </Alert>
      )}

      {!devicesLoading && !devicesError && (
        <>
          <Table striped highlightOnHover withTableBorder>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>ID</Table.Th>
                <Table.Th>Class</Table.Th>
                <Table.Th>Fabrication ID</Table.Th>
                <Table.Th>Lifecycle state</Table.Th>
                <Table.Th>Created</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {devices.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={5}>
                    <Text c="dimmed" fz="sm" ta="center" py="md">
                      No devices
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )}
              {devices.map((d) => (
                <Table.Tr key={d.id}>
                  <Table.Td>
                    <Link to="/catalog/devices/$id" params={{ id: d.id }}>
                      <Anchor component="span" fz="sm" ff="monospace">
                        {d.id.slice(0, 8)}…
                      </Anchor>
                    </Link>
                  </Table.Td>
                  <Table.Td fz="sm">{d.device_class}</Table.Td>
                  <Table.Td fz="sm">{d.fabrication_id ?? '—'}</Table.Td>
                  <Table.Td fz="sm">{d.lifecycle_state ?? '—'}</Table.Td>
                  <Table.Td fz="sm">{new Date(d.created_at).toLocaleDateString()}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>

          <Group mt="md">
            {devicesData?.next_cursor && (
              <Button variant="subtle" onClick={handleLoadMore} loading={devicesLoading}>
                Load more
              </Button>
            )}
            {cursor !== undefined && (
              <Button variant="subtle" color="gray" onClick={handleReset}>
                Reset
              </Button>
            )}
          </Group>
        </>
      )}
    </Box>
  )
}
