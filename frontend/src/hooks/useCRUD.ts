import { useState, useCallback, useEffect } from 'react'
import { message, FormInstance } from 'antd'

export interface CrudState<T> {
  items: T[]
  loading: boolean
  modalOpen: boolean
  editing: T | null
}

export interface CrudHandlers<T, CreateData, UpdateData> {
  items: T[]
  loading: boolean
  modalOpen: boolean
  editing: T | null
  setItems: (items: T[] | ((prev: T[]) => T[])) => void
  openCreate: () => void
  openEdit: (item: T) => void
  closeModal: () => void
  handleCreate: (values: CreateData) => Promise<void>
  handleUpdate: (values: UpdateData) => Promise<void>
  handleDelete: (id: number) => Promise<void>
  refresh: () => Promise<void>
}

interface UseCrudOptions<T, CreateData, UpdateData> {
  loadData: () => Promise<T[]>
  createItem: (data: CreateData) => Promise<unknown>
  updateItem: (id: number, data: UpdateData) => Promise<unknown>
  deleteItem: (id: number) => Promise<unknown>
  entityName: string
  form: FormInstance
  messages?: {
    created?: string
    updated?: string
    deleted?: string
    failCreate?: string
    failUpdate?: string
    failDelete?: string
  }
}

export function useCrud<T, CreateData, UpdateData>(options: UseCrudOptions<T, CreateData, UpdateData>): CrudHandlers<T, CreateData, UpdateData> {
  const { loadData, createItem, updateItem, deleteItem, entityName, form, messages: customMessages } = options

  const [state, setState] = useState<CrudState<T>>({
    items: [],
    loading: true,
    modalOpen: false,
    editing: null,
  })

  const defaultMessages = {
    created: `${entityName} created`,
    updated: `${entityName} updated`,
    deleted: `${entityName} deleted`,
    failCreate: `Failed to create ${entityName}`,
    failUpdate: `Failed to update ${entityName}`,
    failDelete: `Failed to delete ${entityName}`,
  }

  const msgs = { ...defaultMessages, ...customMessages }

  const refresh = useCallback(async () => {
    setState(prev => ({ ...prev, loading: true }))
    try {
      const items = await loadData()
      setState(prev => ({ ...prev, items }))
    } finally {
      setState(prev => ({ ...prev, loading: false }))
    }
  }, [loadData])

  useEffect(() => {
    refresh()
  }, [refresh])

  const openCreate = useCallback(() => {
    setState(prev => ({ ...prev, modalOpen: true, editing: null }))
    form.resetFields()
  }, [form])

  const openEdit = useCallback((item: T) => {
    setState(prev => ({ ...prev, modalOpen: true, editing: item }))
  }, [])

  const closeModal = useCallback(() => {
    setState(prev => ({ ...prev, modalOpen: false, editing: null }))
    form.resetFields()
  }, [form])

  const handleCreate = useCallback(
    async (values: CreateData) => {
      try {
        await createItem(values)
        message.success(msgs.created)
        closeModal()
        refresh()
      } catch (e: unknown) {
        const err = e as { response?: { data?: { error?: string } } }
        message.error(err.response?.data?.error || msgs.failCreate)
      }
    },
    [createItem, msgs.created, msgs.failCreate, closeModal, refresh],
  )

  const handleUpdate = useCallback(
    async (values: UpdateData) => {
      if (!state.editing) return
      const id = ((state.editing as unknown) as { id: number }).id
      try {
        await updateItem(id, values)
        message.success(msgs.updated)
        closeModal()
        refresh()
      } catch (e: unknown) {
        const err = e as { response?: { data?: { error?: string } } }
        message.error(err.response?.data?.error || msgs.failUpdate)
      }
    },
    [state.editing, updateItem, msgs.updated, msgs.failUpdate, closeModal, refresh],
  )

  const handleDelete = useCallback(
    async (id: number) => {
      try {
        await deleteItem(id)
        message.success(msgs.deleted)
        refresh()
      } catch (e: unknown) {
        const err = e as { response?: { data?: { error?: string } } }
        message.error(err.response?.data?.error || msgs.failDelete)
      }
    },
    [deleteItem, msgs.deleted, msgs.failDelete, refresh],
  )

  return {
    ...state,
    setItems: (items) => setState(prev => ({
      ...prev,
      items: typeof items === 'function' ? (items as (prev: T[]) => T[])(prev.items) : items,
    })),
    openCreate,
    openEdit,
    closeModal,
    handleCreate,
    handleUpdate,
    handleDelete,
    refresh,
  }
}