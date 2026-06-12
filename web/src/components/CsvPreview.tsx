import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Alert, Anchor, Box, Loader, ScrollArea, Table, Text } from '@mantine/core'
import { getToken } from '@/api/client'

const MAX_LINES = 200

interface CsvPreviewProps {
  runId: string
  fileId: number
  filename: string
}

interface ParsedCsv {
  headers: string[]
  rows: string[][]
  truncated: boolean
}

function parseCsv(text: string): ParsedCsv {
  const lines = text.split('\n').filter((l) => l.trim() !== '')
  const truncated = lines.length > MAX_LINES
  const limited = lines.slice(0, MAX_LINES)
  const [headerLine, ...dataLines] = limited
  const headers = (headerLine ?? '').split(',').map((h) => h.trim())
  const rows = dataLines.map((l) => l.split(',').map((c) => c.trim()))
  return { headers, rows, truncated }
}

export function CsvPreview({ runId, fileId, filename }: CsvPreviewProps) {
  const [open, setOpen] = useState(false)

  const fileUrl = `/v1/runs/${runId}/files/${fileId}`
  const downloadUrl = fileUrl

  const { data, isLoading, isError } = useQuery<ParsedCsv>({
    queryKey: ['csv', runId, fileId],
    queryFn: async () => {
      const token = getToken()
      const res = await fetch(fileUrl, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const text = await res.text()
      return parseCsv(text)
    },
    enabled: open,
    staleTime: 5 * 60 * 1000,
  })

  return (
    <Box mb="sm">
      <Text
        fz="sm"
        c="blue"
        style={{ cursor: 'pointer', display: 'inline-block' }}
        onClick={() => setOpen((o) => !o)}
      >
        {open ? '▾' : '▸'} {filename}
      </Text>

      {open && (
        <Box mt="xs">
          {isLoading && <Loader size="xs" />}
          {isError && (
            <Alert color="red" fz="sm">
              Failed to load CSV.
            </Alert>
          )}
          {data && (
            <>
              {data.truncated && (
                <Text fz="xs" c="dimmed" mb="xs">
                  Showing first {MAX_LINES} lines.{' '}
                  <Anchor href={downloadUrl} target="_blank" rel="noopener noreferrer" fz="xs">
                    Download full file
                  </Anchor>
                </Text>
              )}
              <ScrollArea.Autosize mah={320} type="auto">
                <Table withTableBorder fz="xs" withColumnBorders style={{ minWidth: 400 }}>
                  <Table.Thead>
                    <Table.Tr>
                      {data.headers.map((h, i) => (
                        <Table.Th key={i}>{h}</Table.Th>
                      ))}
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.rows.map((row, ri) => (
                      <Table.Tr key={ri}>
                        {row.map((cell, ci) => (
                          <Table.Td key={ci}>{cell}</Table.Td>
                        ))}
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </ScrollArea.Autosize>
              {!data.truncated && (
                <Text fz="xs" c="dimmed" mt="xs">
                  <Anchor href={downloadUrl} target="_blank" rel="noopener noreferrer">
                    Download {filename}
                  </Anchor>
                </Text>
              )}
            </>
          )}
        </Box>
      )}
    </Box>
  )
}
