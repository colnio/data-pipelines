import { Box, Title, Table, Text, Anchor, Alert, Loader } from '@mantine/core'
import { Link } from '@tanstack/react-router'
import { useReviewsQuery } from '@/api/queries'
import { StateBadge } from '@/components/StateBadge'

export function ReviewsPage() {
  const { data, isLoading, isError, error } = useReviewsQuery()
  const reviews = data?.reviews ?? []

  return (
    <Box>
      <Title order={3} mb="md">
        Pending Reviews
      </Title>

      {isLoading && <Loader size="sm" />}
      {isError && (
        <Alert color="red">
          {error instanceof Error ? error.message : 'Failed to load reviews'}
        </Alert>
      )}

      {!isLoading && !isError && (
        <Table striped highlightOnHover withTableBorder data-testid="reviews-table">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Run ID</Table.Th>
              <Table.Th>State</Table.Th>
              <Table.Th>Measurement type</Table.Th>
              <Table.Th>Sample</Table.Th>
              <Table.Th>Key metrics</Table.Th>
              <Table.Th>Declared at</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {reviews.length === 0 && (
              <Table.Tr>
                <Table.Td colSpan={6}>
                  <Text c="dimmed" fz="sm" ta="center" py="md">
                    No runs awaiting review
                  </Text>
                </Table.Td>
              </Table.Tr>
            )}
            {reviews.map(({ run, latest_artifact }) => {
              // Show a short summary of metrics if present
              let metricsPreview = '—'
              if (latest_artifact?.metrics_json) {
                try {
                  const m = latest_artifact.metrics_json as Record<string, unknown>
                  const keys = Object.keys(m).slice(0, 3)
                  metricsPreview = keys.map((k) => `${k}: ${m[k]}`).join(', ')
                } catch {
                  metricsPreview = '(see detail)'
                }
              }

              return (
                <Table.Tr key={run.id}>
                  <Table.Td>
                    <Link to="/reviews/$runId" params={{ runId: run.id }}>
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
                  <Table.Td fz="sm">{metricsPreview}</Table.Td>
                  <Table.Td fz="sm">
                    {new Date(run.declared_at).toLocaleString()}
                  </Table.Td>
                </Table.Tr>
              )
            })}
          </Table.Tbody>
        </Table>
      )}
    </Box>
  )
}
