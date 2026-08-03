package fblock

import "hash/crc32"

// CRC32 computes the IEEE 802.3 CRC32 (polynomial 0x04C11DB7) of data, per
// docs/docs/archive/03-storage-format.md §2 ("Контрольные суммы: CRC32 (IEEE
// 802.3)").
func CRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}
