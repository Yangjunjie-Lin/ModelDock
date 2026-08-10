import { Building2, FolderKanban } from 'lucide-react'
import type { Organization, Project } from '../lib/v2'

export function TenantSelector({
  organizations,
  organizationID,
  onOrganizationChange,
  projects,
  projectID,
  onProjectChange,
  projectRequired = false,
}: {
  organizations: Organization[]
  organizationID: string
  onOrganizationChange: (value: string) => void
  projects?: Project[]
  projectID?: string
  onProjectChange?: (value: string) => void
  projectRequired?: boolean
}) {
  return <div className="tenant-selector" aria-label="Tenant scope">
    <label><Building2 size={14} /><span>Organization</span><select value={organizationID} onChange={(event) => onOrganizationChange(event.target.value)}><option value="">Select organization</option>{organizations.map((organization) => <option value={organization.id} key={organization.id}>{organization.name}</option>)}</select></label>
    {projects && <label><FolderKanban size={14} /><span>Project{projectRequired ? ' *' : ''}</span><select value={projectID || ''} onChange={(event) => onProjectChange?.(event.target.value)} disabled={!organizationID}><option value="">Select project</option>{projects.map((project) => <option value={project.id} key={project.id}>{project.name}</option>)}</select></label>}
  </div>
}

