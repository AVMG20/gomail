// Attachment bytes of open drafts, kept in IndexedDB (localStorage is far too
// small for them). Every call is best-effort: storage may be unavailable or
// fail, which callers treat as "not stored".

export interface StoredFile {
  id: string
  filename: string
  mimeType: string
  size: number
  dataBase64: string
}

const DB_NAME = 'gomail-drafts'
const STORE = 'files'

let dbPromise: Promise<IDBDatabase | null> | null = null

function openDb(): Promise<IDBDatabase | null> {
  if (dbPromise) return dbPromise
  dbPromise = new Promise<IDBDatabase | null>((resolve) => {
    try {
      if (typeof indexedDB === 'undefined') return resolve(null)
      const req = indexedDB.open(DB_NAME, 1)
      req.onupgradeneeded = () => {
        if (!req.result.objectStoreNames.contains(STORE)) req.result.createObjectStore(STORE)
      }
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => resolve(null)
      req.onblocked = () => resolve(null)
    } catch {
      resolve(null)
    }
  }).then((db) => {
    if (!db) dbPromise = null // try again next time
    return db
  })
  return dbPromise
}

function run<T>(mode: IDBTransactionMode, fn: (s: IDBObjectStore) => IDBRequest<T>): Promise<T | undefined> {
  return openDb().then((db) => new Promise<T | undefined>((resolve) => {
    if (!db) return resolve(undefined)
    try {
      const tx = db.transaction(STORE, mode)
      const req = fn(tx.objectStore(STORE))
      tx.oncomplete = () => resolve(req.result)
      tx.onerror = () => resolve(undefined)
      tx.onabort = () => resolve(undefined)
    } catch {
      resolve(undefined)
    }
  }))
}

/** Store a draft's files (replacing what was stored). Resolves false when it couldn't. */
export async function saveDraftFiles(draftId: string, files: StoredFile[]): Promise<boolean> {
  const done = await run('readwrite', (s) => s.put(files.map((f) => ({ ...f })), draftId))
  return done !== undefined
}

/** A draft's stored files, or null when none are stored (or storage is unavailable). */
export async function loadDraftFiles(draftId: string): Promise<StoredFile[] | null> {
  const v = await run<unknown>('readonly', (s) => s.get(draftId))
  return Array.isArray(v) ? (v as StoredFile[]) : null
}

export async function deleteDraftFiles(draftId: string): Promise<void> {
  await run('readwrite', (s) => s.delete(draftId))
}

/** Forget files of drafts that no longer exist. */
export async function pruneDraftFiles(keep: Set<string>): Promise<void> {
  const keys = await run<IDBValidKey[]>('readonly', (s) => s.getAllKeys())
  for (const k of keys ?? []) if (typeof k === 'string' && !keep.has(k)) await deleteDraftFiles(k)
}
