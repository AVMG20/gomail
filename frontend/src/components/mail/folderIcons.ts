import { ArchiveIcon, FileTextIcon, FolderIcon, InboxIcon, OctagonAlertIcon, SendIcon, StarIcon, Trash2Icon } from '@lucide/vue'
import type { Component } from 'vue'

export const FOLDER_ICONS: Record<string, Component> = {
  inbox: InboxIcon, starred: StarIcon, sent: SendIcon, drafts: FileTextIcon, archive: ArchiveIcon,
  spam: OctagonAlertIcon, trash: Trash2Icon, custom: FolderIcon,
}
