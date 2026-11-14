-- Возврат оборудования - отдельное событие в журнале выдач (журнал только дописывается)
ALTER TABLE issued ADD COLUMN returned boolean NOT NULL DEFAULT false;
