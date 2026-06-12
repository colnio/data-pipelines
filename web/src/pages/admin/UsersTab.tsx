import { useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  Select,
  Stack,
  Table,
  Text,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useAdminUsersQuery, usePatchUserMutation } from '@/api/admin'
import { ApiError } from '@/api/client'
import { useIsAdmin } from '@/auth/useRole'

const STATUS_OPTIONS = [
  { value: '', label: 'All statuses' },
  { value: 'pending', label: 'pending' },
  { value: 'active', label: 'active' },
  { value: 'disabled', label: 'disabled' },
]

const ROLE_OPTIONS = [
  { value: '', label: 'All roles' },
  { value: 'member', label: 'member' },
  { value: 'pi', label: 'pi' },
  { value: 'admin', label: 'admin' },
]

const ROLE_PATCH_OPTIONS = [
  { value: 'member', label: 'member' },
  { value: 'pi', label: 'pi' },
  { value: 'admin', label: 'admin' },
]

const STATUS_COLOR: Record<string, string> = {
  active: 'green',
  pending: 'yellow',
  disabled: 'red',
}

const ROLE_COLOR: Record<string, string> = {
  admin: 'violet',
  pi: 'blue',
  member: 'gray',
}

export function UsersTab() {
  const isAdmin = useIsAdmin()
  const [filterStatus, setFilterStatus] = useState('')
  const [filterRole, setFilterRole] = useState('')

  const { data, isLoading, isError, error } = useAdminUsersQuery({
    status: filterStatus || undefined,
    role: filterRole || undefined,
  })

  const patchMutation = usePatchUserMutation()

  const users = data?.users ?? []

  function handlePatch(
    id: string,
    display: string,
    body: { status?: string; global_role?: string },
  ) {
    patchMutation.mutate(
      { id, body },
      {
        onSuccess: () => {
          notifications.show({
            color: 'green',
            message: `Updated ${display}`,
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
      {/* Filters */}
      <Group align="flex-end" wrap="wrap">
        <Select
          label="Status"
          data={STATUS_OPTIONS}
          value={filterStatus}
          onChange={(v) => setFilterStatus(v ?? '')}
          w={160}
          clearable
        />
        <Select
          label="Role"
          data={ROLE_OPTIONS}
          value={filterRole}
          onChange={(v) => setFilterRole(v ?? '')}
          w={160}
          clearable
        />
      </Group>

      {isLoading && <Loader size="sm" />}
      {isError && (
        <Alert color="red">
          {error instanceof Error ? error.message : 'Failed to load users'}
        </Alert>
      )}

      {!isLoading && !isError && (
        <Table striped highlightOnHover withTableBorder>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Email</Table.Th>
              <Table.Th>Name</Table.Th>
              <Table.Th>Role</Table.Th>
              <Table.Th>Status</Table.Th>
              <Table.Th>Created</Table.Th>
              <Table.Th>Actions</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {users.length === 0 && (
              <Table.Tr>
                <Table.Td colSpan={6}>
                  <Text c="dimmed" fz="sm" ta="center" py="md">
                    No users found
                  </Text>
                </Table.Td>
              </Table.Tr>
            )}
            {users.map((u) => (
              <Table.Tr key={u.id}>
                <Table.Td fz="sm">{u.email}</Table.Td>
                <Table.Td fz="sm">{u.display_name}</Table.Td>
                <Table.Td>
                  {isAdmin ? (
                    <Select
                      data={ROLE_PATCH_OPTIONS}
                      value={u.global_role}
                      onChange={(v) => {
                        if (v && v !== u.global_role) {
                          handlePatch(u.id, u.email, { global_role: v })
                        }
                      }}
                      size="xs"
                      w={100}
                    />
                  ) : (
                    <Badge color={ROLE_COLOR[u.global_role] ?? 'gray'} variant="light" size="sm">
                      {u.global_role}
                    </Badge>
                  )}
                </Table.Td>
                <Table.Td>
                  <Badge color={STATUS_COLOR[u.status] ?? 'gray'} variant="light" size="sm">
                    {u.status}
                  </Badge>
                </Table.Td>
                <Table.Td fz="sm">{new Date(u.created_at).toLocaleDateString()}</Table.Td>
                <Table.Td>
                  <Group gap="xs">
                    {u.status === 'pending' && (
                      <Tooltip
                        label={isAdmin ? 'Activate user' : 'admin only'}
                        disabled={isAdmin}
                      >
                        <Button
                          size="xs"
                          color="green"
                          variant="light"
                          disabled={!isAdmin || patchMutation.isPending}
                          onClick={() => handlePatch(u.id, u.email, { status: 'active' })}
                        >
                          Activate
                        </Button>
                      </Tooltip>
                    )}
                    {u.status !== 'disabled' && (
                      <Tooltip
                        label={isAdmin ? 'Disable user' : 'admin only'}
                        disabled={isAdmin}
                      >
                        <Button
                          size="xs"
                          color="red"
                          variant="light"
                          disabled={!isAdmin || patchMutation.isPending}
                          onClick={() => handlePatch(u.id, u.email, { status: 'disabled' })}
                        >
                          Disable
                        </Button>
                      </Tooltip>
                    )}
                    {u.status === 'disabled' && (
                      <Tooltip
                        label={isAdmin ? 'Re-activate user' : 'admin only'}
                        disabled={isAdmin}
                      >
                        <Button
                          size="xs"
                          color="blue"
                          variant="light"
                          disabled={!isAdmin || patchMutation.isPending}
                          onClick={() => handlePatch(u.id, u.email, { status: 'active' })}
                        >
                          Re-activate
                        </Button>
                      </Tooltip>
                    )}
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  )
}
