# 04: Реализовать кодек TLV-узла/эпилога и переписать fblock/*.go под v2.0

Status: resolved (2026-08-25) — все 4 фазы плана миграции `internal/storage` на v2.0 (`/home/aldishu/.claude/plans/majestic-sleeping-hollerith.md`) выполнены и закоммичены.

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

8. ✅ Фаза 3 (cutover, коммит `823a64b`): `init.go`/`writetxn.go`/`segment.go`/`startup.go`/`consistency.go`/`reader.go`/`unit.go` переключены на v2.0 одним согласованным изменением, `format_version_major = 2`. Заодно найден и исправлен реальный баг выравнивания: `StorageEngine`'s write-verify предполагал offset джобы уже выровненным под `Alignment()` backend'а, что неверно для content-узла v2.0 (его `value` начинается сразу после 29-байтного заголовка, не кратного произвольному выравниванию) — исправлено в `internal/storageengine/engine.go` (`stepWriteLocked`/`writeVerifyChunkLocked`: read-modify-write по наименьшему покрывающему выровненному блоку), плюс отдельно в `recoverPartialWriteV2` (работает до создания `Engine`). Оба места покрыты регрессионными тестами на строгом alignment-enforcing backend'е (`internal/storageengine/engine_test.go`, `internal/storage/consistency_v2_test.go`).
9. ✅ Фаза 4 (очистка, коммит `c7c7fec`): удалены `fblock/header.go`/`epilog.go`/`geometry.go`/`diagnosis.go`; `prolog.go` урезан до `ErrUninitialized`/`HasValidMagicProlog`, `magic.go` — до `MagicProlog`/`MagicTrailer`. Обновлены `03-storage-format.md`, ADR-023, `04-storage-operations.md`, `CLAUDE.md`.

Проверено целиком: `go build`/`vet`/`golangci-lint run ./...` (0 замечаний), полный `go test ./...`, реальный e2e (`go test -tags e2e ./tests/...` — farcd+hls_server пишут/читают на новом формате).

## Осталось

- Косметика, отложено осознанно: суффиксы `_v2` в именах файлов/функций (`assemble_v2.go`, `consistency_v2.go`, `startup_v2.go`, `verifyWriteCompletionV2` и т.п.) избыточны теперь, когда v1.0 удалён, но переименование — механическая правка по многим call sites без функциональной пользы; риск регрессии не оправдан прямо сейчас.
- Не связано с форматом фблока, но всплыло по пути: `TestE2E_FarcAndHlsdRealProcesses`'s проверка graceful shutdown по SIGTERM падает по таймингу примерно в половине запусков (сам путь записи/чтения проходит одинаково в обоих исходах — падает именно ожидание выхода процесса). Нет базовой линии до этой миграции, чтобы подтвердить, что флейк был и раньше — стоит проверить отдельно (вероятно, ingest-retry-loop не сразу реагирует на отмену контекста при SIGTERM).

## Answer

Реализовано полностью — issue закрыт.
