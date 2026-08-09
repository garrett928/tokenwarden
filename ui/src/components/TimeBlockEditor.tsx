import { useState } from 'react'
import type { TimeBlockDTO } from '../api/types'

interface TimeBlockEditorProps {
  label: string
  blocks: TimeBlockDTO[]
  onChange: (blocks: TimeBlockDTO[]) => void
}

const DAY_NAMES = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

function formatTime(minutes: number): string {
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
}

function formatBlock(block: TimeBlockDTO): string {
  const timeStr = `${formatTime(block.start_min)}–${formatTime(block.end_min)}`

  if (!block.days || block.days.length === 0) {
    return `Every day ${timeStr}`
  }

  const dayStrs = block.days
    .sort((a, b) => a - b)
    .map(d => DAY_NAMES[d])
    .join(', ')

  return `${dayStrs} ${timeStr}`
}

export default function TimeBlockEditor({
  label,
  blocks,
  onChange,
}: TimeBlockEditorProps) {
  const [selectedDays, setSelectedDays] = useState<Set<number>>(new Set())
  const [startTime, setStartTime] = useState('')
  const [endTime, setEndTime] = useState('')

  const handleDayToggle = (day: number) => {
    const newDays = new Set(selectedDays)
    if (newDays.has(day)) {
      newDays.delete(day)
    } else {
      newDays.add(day)
    }
    setSelectedDays(newDays)
  }

  const handleAdd = () => {
    if (!startTime || !endTime) return

    const [startH, startM] = startTime.split(':').map(Number)
    const [endH, endM] = endTime.split(':').map(Number)
    const startMin = startH * 60 + startM
    const endMin = endH * 60 + endM

    const newBlock: TimeBlockDTO = {
      start_min: startMin,
      end_min: endMin,
    }

    // Only include days if at least one is selected
    if (selectedDays.size > 0) {
      newBlock.days = Array.from(selectedDays).sort((a, b) => a - b)
    }

    onChange([...blocks, newBlock])

    // Reset form
    setSelectedDays(new Set())
    setStartTime('')
    setEndTime('')
  }

  const handleDelete = (index: number) => {
    onChange(blocks.filter((_, i) => i !== index))
  }

  return (
    <div className="stack">
      <label>
        <strong>{label}</strong>
      </label>

      {/* Existing blocks list */}
      <div className="stack">
        {blocks.length === 0 ? (
          <p className="muted">No blocks configured</p>
        ) : (
          blocks.map((block, idx) => (
            <div key={idx} className="row" style={{ justifyContent: 'space-between' }}>
              <span>{formatBlock(block)}</span>
              <button
                className="danger"
                onClick={() => handleDelete(idx)}
                style={{ padding: '4px 8px', fontSize: '12px' }}
              >
                ×
              </button>
            </div>
          ))
        )}
      </div>

      {/* Add new block form */}
      <div className="stack" style={{ paddingTop: '8px', borderTop: '1px solid var(--border)' }}>
        <div style={{ fontSize: '12px', color: 'var(--fg-muted)' }}>Add new block</div>

        {/* Day checkboxes */}
        <div className="row" style={{ flexWrap: 'wrap', gap: '12px' }}>
          {DAY_NAMES.map((name, idx) => (
            <label
              key={idx}
              style={{ display: 'flex', alignItems: 'center', gap: '4px', cursor: 'pointer' }}
            >
              <input
                type="checkbox"
                checked={selectedDays.has(idx)}
                onChange={() => handleDayToggle(idx)}
              />
              <span style={{ fontSize: '12px' }}>{name}</span>
            </label>
          ))}
        </div>

        {/* Time inputs */}
        <div className="row">
          <input
            type="time"
            value={startTime}
            onChange={(e) => setStartTime(e.target.value)}
            placeholder="Start time"
          />
          <span>to</span>
          <input
            type="time"
            value={endTime}
            onChange={(e) => setEndTime(e.target.value)}
            placeholder="End time"
          />
          <button
            className="primary"
            onClick={handleAdd}
            disabled={!startTime || !endTime}
          >
            Add
          </button>
        </div>
      </div>
    </div>
  )
}
