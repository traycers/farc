# 04: Реализовать кодек TLV-узла/эпилога и переписать fblock/*.go под v2.0

Status: in progress — см. план миграции `internal/storage` на v2.0: `/home/aldishu/.claude/plans/majestic-sleeping-hollerith.md` (Фазы 1–4)

Зависимости (01, 02, 03) resolved 2026-08-24 — структура зафиксирована в ADR-023 (Принято) и `03-storage-format.md` §12.

## Сделано

1. ✅ Кодек TLV-узла (`fblock/v2/node.go`) — `EncodeNode`/`DecodeNode`, плюс потоковый вариант `EncodeNodeHeader`/`EncodeNodeTail`/`NodeTotalSize` для узла, чей `value` льётся через `Append` (content), а не известен целиком заранее.
2. ✅ Кодек эпилога (`fblock/v2/epilog.go`) — `EncodeEpilog`/`DecodeEpilog`. Уточнение сверх ADR-023: `crc32` строки — копия `crc32_value` узла, не CRC32 узла целиком (избежали CRC32-combine арифметики; см. ADR-023 §12.5, обновлено 2026-08-24).
3. ✅ Урезанная фиксированная часть пролога (`fblock/v2/prolog.go`) — без `params_size`/`catalog_size`.
4. ✅ Сборка дерева из 5 узлов (`fblock/v2/tree.go`: `AssembleFblock`/`ReadFblock`).
5. ✅ Прогрессивная перезапись эпилога через реальный `storageengine.Engine` (`internal/storage/assemble_v2.go`: `writeNodeAndEpilogRow`, `writeFblockV2Progressive`) — без новой машинерии в движке.
6. ✅ Потоковая запись content-узла через `EnqueueOpenWrite`/`Append`/`Close` (`internal/storage/assemble_v2.go`: `nodeWriterV2`) — header/трейлер финализируются на `close`, `MagicTrailer`-механизм ADR-017 не тронут.

7. ✅ Аддитивный v2-путь диагностики/восстановления (`internal/storage/consistency_v2.go`: `verifyWriteCompletionV2`, `recoverPartialWriteV2`; `startup_v2.go`: `probeGeometryV2`, `scanForFreshestCatalogV2`). Диагностика по прогрессивному `count` (0..5). Восстановление покрывает оба реальных случая: `count==3` (content ещё открыт — `FindTrailer`/`DecodeContentPartial`, не изменилось от v1.0) и `count==4` (content завершён, только TOC отсутствует — полный `DecodeContentWithOffsets`, без поиска трейлера).

Всё выше — аддитивно, не подключено к реальному пути записи/чтения (`init.go`/`segment.go`/`writetxn.go`/`consistency.go`/`startup.go`/`reader.go` всё ещё на v1.0). Числовые коды `type` — по порядку объявления (`root=0,params=1,catalog=2,content=3,toc=4`). Весь новый код проходит `golangci-lint run ./...` без замечаний (репозиторий целиком).

## Осталось

- Фаза 3: cutover — переключить `init.go`/`writetxn.go`/`segment.go`/`startup.go`/`consistency.go`/`reader.go`/`unit.go` одним согласованным изменением, `format_version_major = 2`.
- Фаза 4: удалить v1.0-only framing (`fblock/prolog.go`, `header.go`, `epilog.go`, `geometry.go`), обновить документацию.

## Answer

(не заполнено)
