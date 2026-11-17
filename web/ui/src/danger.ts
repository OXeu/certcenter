import type { createForm } from './forms.ts'
export const dangerOperations = {
  deleteDNSProvider: 'common.delete',
  deleteUpstream: 'common.delete',
  issueCertificate: 'cert.issue',
  deployCertificate: 'cert.deploy',
  signOut: 'nav.signOut',
  deleteUser: 'common.delete',
  rotateKey: 'projects.rotate',
  deleteProject: 'common.delete',
  deleteEnvironment: 'common.delete',
  deleteItem: 'common.delete',
  revokeKey: 'projects.revoke',
  approveProposal: 'projects.approve',
  rejectProposal: 'projects.reject',
} as const
export type DangerOperation = keyof typeof dangerOperations
const confirmedAction = Symbol('confirmedAction')
export type DangerAction = {
  [confirmedAction]: true
  id: DangerOperation
  label: string
  request: () => void
}
export function dangerAction(
  form: ReturnType<typeof createForm>,
  id: DangerOperation,
  label: string,
  target: string,
  execute: () => Promise<void>,
): DangerAction {
  return {
    [confirmedAction]: true,
    id,
    label,
    request() {
      form.show(`${label}: ${target}`, [], {}, execute)
      form.danger = true
    },
  }
}
