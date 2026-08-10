import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Building2, MoreHorizontal, Plus, Trash2, UserPlus } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import { type Organization, type OrganizationMember, useAdminTenantScope, v2Paths } from '../lib/v2'
import { Badge, Button, DataTable, type Column, Drawer, EmptyState, ErrorState, Form, Modal, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

export function OrganizationsPage() {
  const scope = useAdminTenantScope()
  const [createOpen, setCreateOpen] = useState(false)
  const [detail, setDetail] = useState<Organization | null>(null)
  const [form, setForm] = useState({ name: '', slug: '' })
  const client = useQueryClient()
  const toast = useToast()

  useEffect(() => {
    if (detail) setDetail(scope.organizationRows.find((row) => row.id === detail.id) || null)
  }, [detail?.id, scope.organizationRows])

  const create = useMutation({
    mutationFn: () => api<Organization>(v2Paths.organizations, { method: 'POST', body: JSON.stringify({ ...form, status: 'ACTIVE' }) }),
    onSuccess: (organization) => {
      setCreateOpen(false)
      setForm({ name: '', slug: '' })
      scope.setOrganizationID(organization.id)
      void client.invalidateQueries({ queryKey: ['v2-organizations'] })
      toast('Organization created')
    },
  })
  const updateStatus = useMutation({
    mutationFn: ({ organization, status }: { organization: Organization; status: string }) => api<Organization>(v2Paths.organization(organization.id), { method: 'PUT', body: JSON.stringify({ ...organization, status }) }),
    onSuccess: (organization) => {
      setDetail(organization)
      void client.invalidateQueries({ queryKey: ['v2-organizations'] })
      toast(`Organization ${organization.status === 'ACTIVE' ? 'activated' : 'disabled'}`)
    },
  })
  const archive = useMutation({
    mutationFn: (organizationID: string) => api(v2Paths.organization(organizationID), { method: 'DELETE' }),
    onSuccess: () => {
      setDetail(null)
      void client.invalidateQueries({ queryKey: ['v2-organizations'] })
      toast('Organization archived')
    },
  })

  const columns: Column<Organization>[] = [
    { key: 'name', label: 'Organization', render: (row) => <div className="primary-cell"><strong>{row.name}</strong><code>{row.slug}</code></div> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'id', label: 'ID', render: (row) => <code className="inline-code">{row.id}</code> },
    { key: 'created', label: 'Created', render: (row) => <span className="muted-cell">{formatDate(row.created_at)}</span> },
    { key: 'actions', label: '', className: 'action-cell', render: (row) => <Button size="sm" variant="ghost" onClick={() => { scope.setOrganizationID(row.id); setDetail(row) }} aria-label={`Manage ${row.name}`}><MoreHorizontal size={15} /></Button> },
  ]

  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><span className="live-dot" />V2 TENANCY</div><h1>Organizations</h1><p>Create tenant boundaries and control organization membership and status.</p></div><Button variant="primary" onClick={() => setCreateOpen(true)}><Plus size={15} />Create organization</Button></div>
    <div className="security-card tenant-safety"><Building2 size={20} /><div><strong>Organization status fails closed</strong><p>Disabling or archiving an organization immediately invalidates project API keys under it.</p></div><Badge tone="success">Enforced</Badge></div>
    <section className="resource-panel">
      {scope.organizations.isLoading && <div className="panel-pad"><Skeleton rows={7} /></div>}
      {scope.organizations.isError && <div className="panel-pad"><ErrorState error={scope.organizations.error} onRetry={() => scope.organizations.refetch()} /></div>}
      {scope.organizations.isSuccess && scope.organizationRows.length === 0 && <EmptyState title="No organizations" description="Create an organization before adding projects or issuing project-scoped keys." action={<Button variant="primary" onClick={() => setCreateOpen(true)}><Plus size={15} />Create organization</Button>} />}
      {scope.organizationRows.length > 0 && <DataTable columns={columns} rows={scope.organizationRows} rowKey={(row) => row.id} />}
    </section>

    <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="Create organization" description="The signed-in administrator becomes the initial owner." footer={<><Button onClick={() => setCreateOpen(false)}>Cancel</Button><SubmitButton form="create-organization" pending={create.isPending}>Create organization</SubmitButton></>}>
      <Form id="create-organization" className="form-grid" onSubmit={() => create.mutateAsync()}><label><span>Name *</span><input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="Acme Platform" /></label><label><span>Slug *</span><input required pattern="[a-z0-9][a-z0-9-]*" value={form.slug} onChange={(event) => setForm({ ...form, slug: event.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-') })} placeholder="acme-platform" /></label>{create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Unable to create organization.'}</div>}</Form>
    </Modal>

    <Drawer open={Boolean(detail)} onClose={() => setDetail(null)} title="Organization governance">
      {detail && <><div className="detail-hero"><span className="provider-glyph"><Building2 size={15} /></span><div><strong>{detail.name}</strong><code>{detail.slug}</code></div><StatusBadge value={detail.status} /></div><div className="detail-list"><div><span>Organization ID</span><strong>{detail.id}</strong></div><div><span>Created</span><strong>{formatDate(detail.created_at)}</strong></div><div><span>Updated</span><strong>{formatDate(detail.updated_at)}</strong></div></div><OrganizationMembers organization={detail} /><div className="drawer-actions"><Button onClick={() => updateStatus.mutate({ organization: detail, status: detail.status === 'ACTIVE' ? 'DISABLED' : 'ACTIVE' })} disabled={updateStatus.isPending}>{detail.status === 'ACTIVE' ? 'Disable organization' : 'Enable organization'}</Button>{detail.status !== 'ARCHIVED' && <Button variant="danger" onClick={() => archive.mutate(detail.id)} disabled={archive.isPending}><Trash2 size={13} />Archive</Button>}</div>{(updateStatus.isError || archive.isError) && <div className="form-error">{String(updateStatus.error || archive.error)}</div>}</>}
    </Drawer>
  </div>
}

function OrganizationMembers({ organization }: { organization: Organization }) {
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ user_id: '', role: 'MEMBER', status: 'ACTIVE' })
  const client = useQueryClient()
  const toast = useToast()
  const result = useQuery({
    queryKey: ['v2-organization-members', organization.id],
    queryFn: () => api<unknown>(v2Paths.organizationMembers(organization.id)).then(asPage<OrganizationMember>),
  })
  const save = useMutation({
    mutationFn: () => api(v2Paths.organizationMember(organization.id, form.user_id), { method: 'PUT', body: JSON.stringify({ role: form.role, status: form.status }) }),
    onSuccess: () => {
      setOpen(false)
      setForm({ user_id: '', role: 'MEMBER', status: 'ACTIVE' })
      void client.invalidateQueries({ queryKey: ['v2-organization-members', organization.id] })
      toast('Organization membership saved')
    },
  })
  const remove = useMutation({
    mutationFn: (userID: string) => api(v2Paths.organizationMember(organization.id, userID), { method: 'DELETE' }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['v2-organization-members', organization.id] }); toast('Organization member removed') },
  })
  const rows = result.data?.items || []

  return <section className="drawer-section"><div className="section-heading"><div><strong>Members</strong><small>Organization-wide roles and status</small></div><Button size="sm" onClick={() => setOpen(true)}><UserPlus size={13} />Add</Button></div>{result.isLoading && <Skeleton rows={3} />}{result.isError && <ErrorState error={result.error} onRetry={() => result.refetch()} />}{rows.length === 0 && result.isSuccess && <EmptyState title="No members" />}{rows.map((member) => <div className="member-row" key={member.user_id}><span className="avatar">{(member.display_name || member.email || 'U').slice(0, 2).toUpperCase()}</span><div><strong>{member.display_name || member.email || member.user_id}</strong><small>{member.email || member.user_id}</small></div><Badge tone="violet">{member.role}</Badge><StatusBadge value={member.status} />{member.role !== 'OWNER' && <Button size="sm" variant="ghost" onClick={() => remove.mutate(member.user_id)} aria-label="Remove member"><Trash2 size={13} /></Button>}</div>)}
    <Modal open={open} onClose={() => setOpen(false)} title="Add organization member" description="The user must already exist in RelayDock." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="organization-member" pending={save.isPending}>Save member</SubmitButton></>}><Form id="organization-member" className="form-grid" onSubmit={() => save.mutateAsync()}><label className="full-span"><span>User ID *</span><input required value={form.user_id} onChange={(event) => setForm({ ...form, user_id: event.target.value })} placeholder="RelayDock user ID" /></label><label><span>Role *</span><select value={form.role} onChange={(event) => setForm({ ...form, role: event.target.value })}><option value="VIEWER">Viewer</option><option value="MEMBER">Member</option><option value="ADMIN">Administrator</option><option value="OWNER">Owner</option></select></label><label><span>Status *</span><select value={form.status} onChange={(event) => setForm({ ...form, status: event.target.value })}><option value="ACTIVE">Active</option><option value="DISABLED">Disabled</option></select></label>{save.isError && <div className="form-error full-span">{save.error instanceof Error ? save.error.message : 'Unable to save membership.'}</div>}</Form></Modal>
  </section>
}
