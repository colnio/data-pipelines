import { Alert, Box, Divider, Image, Loader, SimpleGrid, Text, Title } from '@mantine/core'
import { useRunArtifactsQuery } from '@/api/artifacts'
import { NotebookViewer } from './NotebookViewer'

interface ArtifactGalleryProps {
  runId: string
}

export function ArtifactGallery({ runId }: ArtifactGalleryProps) {
  const { data, isLoading, isError } = useRunArtifactsQuery(runId)

  if (isLoading) return <Loader size="xs" />
  if (isError)
    return (
      <Alert color="orange" fz="sm">
        Could not load artifacts.
      </Alert>
    )

  const artifacts = data?.artifacts ?? []
  if (artifacts.length === 0) {
    return (
      <Text fz="sm" c="dimmed">
        No artifacts.
      </Text>
    )
  }

  const plots = artifacts.filter((a) => a.kind === 'plot')
  const notebooks = artifacts.filter((a) => a.kind === 'notebook')

  return (
    <Box>
      {plots.length > 0 && (
        <>
          <Title order={6} mb="sm">
            Plots
          </Title>
          <SimpleGrid cols={{ base: 1, sm: 2, md: 3 }} spacing="sm" mb="md">
            {plots.map((a) => (
              <Box key={a.id} style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: 6, overflow: 'hidden' }}>
                {/* url is pre-signed — use directly in <img src>, no auth header */}
                <Image src={a.url} alt={a.filename} fit="contain" />
                <Text fz="xs" c="dimmed" p="xs" ta="center">
                  {a.filename}
                </Text>
              </Box>
            ))}
          </SimpleGrid>
        </>
      )}

      {notebooks.length > 0 && (
        <>
          {plots.length > 0 && <Divider mb="md" />}
          {notebooks.map((a) => (
            <Box key={a.id} mb="md">
              <NotebookViewer url={a.url} filename={a.filename} />
            </Box>
          ))}
        </>
      )}
    </Box>
  )
}
