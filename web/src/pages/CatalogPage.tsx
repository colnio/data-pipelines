import { useState } from 'react'
import { Alert, Anchor, Box, Button, Group, Loader, Table, Text, Title } from '@mantine/core'
import { Link } from '@tanstack/react-router'
import { useSamplesQuery, type Sample } from '@/api/catalog'

export function CatalogPage() {
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [allSamples, setAllSamples] = useState<Sample[]>([])

  const { data, isLoading, isError, error } = useSamplesQuery(cursor)

  // Accumulate samples across pages
  const freshSamples = data?.samples ?? []
  const combinedSamples = cursor === undefined
    ? freshSamples
    : [...allSamples, ...freshSamples]

  const handleLoadMore = () => {
    if (data?.next_cursor) {
      setAllSamples(combinedSamples)
      setCursor(data.next_cursor)
    }
  }

  const handleReset = () => {
    setAllSamples([])
    setCursor(undefined)
  }

  const samples = combinedSamples

  return (
    <Box>
      <Title order={3} mb="md">
        Catalog — Samples
      </Title>

      {isLoading && <Loader size="sm" />}
      {isError && (
        <Alert color="red" mb="md">
          {error instanceof Error ? error.message : 'Failed to load samples'}
        </Alert>
      )}

      {!isLoading && !isError && (
        <>
          <Table striped highlightOnHover withTableBorder>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>ID</Table.Th>
                <Table.Th>Material stack</Table.Th>
                <Table.Th>Fabrication batch</Table.Th>
                <Table.Th>Created</Table.Th>
                <Table.Th>Notes</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {samples.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={5}>
                    <Text c="dimmed" fz="sm" ta="center" py="md">
                      No samples found
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )}
              {samples.map((s) => (
                <Table.Tr key={s.id}>
                  <Table.Td>
                    <Link to="/catalog/samples/$id" params={{ id: s.id }}>
                      <Anchor component="span" fz="sm" ff="monospace">
                        {s.id.slice(0, 8)}…
                      </Anchor>
                    </Link>
                  </Table.Td>
                  <Table.Td fz="sm">{s.material_stack ?? '—'}</Table.Td>
                  <Table.Td fz="sm">{s.fabrication_batch ?? '—'}</Table.Td>
                  <Table.Td fz="sm">{new Date(s.created_at).toLocaleDateString()}</Table.Td>
                  <Table.Td fz="sm">{s.notes ?? '—'}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>

          <Group mt="md">
            {data?.next_cursor && (
              <Button variant="subtle" onClick={handleLoadMore} loading={isLoading}>
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
