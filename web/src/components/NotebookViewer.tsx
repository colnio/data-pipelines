import { useQuery } from '@tanstack/react-query'
import { Alert, Anchor, Box, Loader, ScrollArea, Stack, Text, Title } from '@mantine/core'

// ─── Notebook cell types (subset of nbformat v4) ─────────────────────────────

interface NotebookOutput {
  output_type: string
  text?: string | string[]
  data?: Record<string, string | string[]>
}

interface NotebookCell {
  cell_type: 'code' | 'markdown' | 'raw'
  source: string | string[]
  outputs?: NotebookOutput[]
}

interface NotebookDoc {
  cells: NotebookCell[]
  nbformat?: number
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function joinSource(s: string | string[]): string {
  return Array.isArray(s) ? s.join('') : s
}

function CellOutputs({ outputs }: { outputs: NotebookOutput[] }) {
  return (
    <Stack gap={4} mt={4}>
      {outputs.map((out, i) => {
        if (out.output_type === 'stream' && out.text) {
          return (
            <Box
              key={i}
              style={{
                fontFamily: 'monospace',
                fontSize: 12,
                whiteSpace: 'pre-wrap',
                background: 'var(--mantine-color-gray-0)',
                padding: '4px 8px',
                borderRadius: 4,
              }}
            >
              {joinSource(out.text)}
            </Box>
          )
        }
        if ((out.output_type === 'display_data' || out.output_type === 'execute_result') && out.data) {
          const png = out.data['image/png']
          if (png) {
            const src = `data:image/png;base64,${Array.isArray(png) ? png.join('') : png}`
            return (
              <Box key={i} style={{ maxWidth: '100%' }}>
                <img src={src} alt={`cell output ${i}`} style={{ maxWidth: '100%', height: 'auto' }} />
              </Box>
            )
          }
          const plain = out.data['text/plain']
          if (plain) {
            return (
              <Box
                key={i}
                style={{
                  fontFamily: 'monospace',
                  fontSize: 12,
                  whiteSpace: 'pre-wrap',
                  background: 'var(--mantine-color-gray-0)',
                  padding: '4px 8px',
                  borderRadius: 4,
                }}
              >
                {joinSource(plain)}
              </Box>
            )
          }
        }
        return null
      })}
    </Stack>
  )
}

// ─── Component ────────────────────────────────────────────────────────────────

interface NotebookViewerProps {
  /** Pre-signed URL — no auth header needed */
  url: string
  filename?: string
}

export function NotebookViewer({ url, filename }: NotebookViewerProps) {
  const { data, isLoading, isError } = useQuery<NotebookDoc>({
    queryKey: ['notebook', url],
    queryFn: () => fetch(url).then((r) => r.json() as Promise<NotebookDoc>),
    staleTime: 5 * 60 * 1000,
  })

  if (isLoading) return <Loader size="xs" />
  if (isError)
    return (
      <Alert color="orange" fz="sm">
        Could not load notebook.
      </Alert>
    )
  if (!data?.cells) return null

  return (
    <Box>
      <Title order={6} mb="xs">
        Notebook {filename && <Text span c="dimmed" fz="xs"> — {filename}</Text>}
        {'  '}
        <Anchor href={url} target="_blank" rel="noopener noreferrer" fz="xs">
          Download .ipynb
        </Anchor>
      </Title>
      <ScrollArea.Autosize mah={600} type="scroll">
        <Stack gap="xs">
          {data.cells.map((cell, idx) => (
            <Box
              key={idx}
              style={{
                border: '1px solid var(--mantine-color-gray-3)',
                borderRadius: 6,
                padding: '8px 12px',
                background: cell.cell_type === 'code'
                  ? 'var(--mantine-color-dark-8, #f8f9fa)'
                  : undefined,
              }}
            >
              {cell.cell_type === 'markdown' && (
                <Text fz="sm" style={{ whiteSpace: 'pre-wrap' }}>
                  {joinSource(cell.source)}
                </Text>
              )}
              {(cell.cell_type === 'code' || cell.cell_type === 'raw') && (
                <>
                  <Box
                    style={{
                      fontFamily: 'monospace',
                      fontSize: 12,
                      whiteSpace: 'pre-wrap',
                      wordBreak: 'break-word',
                    }}
                  >
                    {joinSource(cell.source)}
                  </Box>
                  {cell.outputs && cell.outputs.length > 0 && (
                    <CellOutputs outputs={cell.outputs} />
                  )}
                </>
              )}
            </Box>
          ))}
        </Stack>
      </ScrollArea.Autosize>
    </Box>
  )
}
