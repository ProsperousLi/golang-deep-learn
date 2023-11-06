基于 go版本 1.21.0 解析

// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

// This file contains the implementation of Go's map type.
// (go的map类型的源码实现如下：)
//
// A map is just a hash table. The data is arranged
// into an array of buckets. Each bucket contains up to
// 8 key/elem pairs. The low-order bits of the hash are
// used to select a bucket. Each bucket contains a few
// high-order bits of each hash to distinguish the entries
// within a single bucket.

TODO
/*  map是一个hash表。
	数据被分布在数组桶里。
	每个桶的数据上限为8个键值对。
	hash表的每个hash值的低位（低？位）是用来选择数据要存在哪个桶的。
	hash表的每个hash值的高位（高？位）是定位单个桶中键值对在哪个位置
*/

//
// If more than 8 keys hash to a bucket, we chain on
// extra buckets.
// (如果每个桶的数据超过8个键值对，将会以链表形式，在桶的后面追加额外桶)
//
// When the hashtable grows, we allocate a new array
// of buckets twice as big. Buckets are incrementally
// copied from the old bucket array to the new bucket array.
//

/*
	当数据在hash表中存储满了，即原有的map的桶的数据满了需要扩容时，go将会申请原有的桶大小的两倍的新的数据桶。
	旧数据桶的数据将会逐步拷贝到新数据桶中。
*/

// Map iterators walk through the array of buckets and
// return the keys in walk order (bucket #, then overflow
    // chain order, then bucket index).  To maintain iteration
// semantics, we never move keys within their bucket (if
// we did, keys might be returned 0 or 2 times).  When
// growing the table, iterators remain iterating through the
// old table and must check the new table if the bucket
// they are iterating through has been moved ("evacuated")
// to the new table.

===========
/*
	map 将会按照桶的遍历顺序返回 key (桶 #, 溢出桶, 索引桶(??)).
	为了保证迭代的语义(逻辑)正确,桶里的key的顺序/位置不会改变（如果改变，map查找到key可能查到0个或者2个）.
	当发生扩容创建了新的数据桶，map在遍历旧桶时，需要去新的数据桶中检测与旧桶对应的新的数桶是否已经将数据迁移完毕（evacuated 状态）
*/

// Picking loadFactor: too large and we have lots of overflow
// buckets, too small and we waste a lot of space. I wrote
// a simple program to check some stats for different loads:
// (64-bit, 8 byte keys and elems)
//  loadFactor    %overflow  bytes/entry     hitprobe    missprobe
//        4.00         2.13        20.77         3.00         4.00
//        4.50         4.05        17.30         3.25         4.50
//        5.00         6.85        14.77         3.50         5.00
//        5.50        10.55        12.94         3.75         5.50
//        6.00        15.27        11.67         4.00         6.00
//        6.50        20.90        10.79         4.25         6.50
//        7.00        27.14        10.15         4.50         7.00
//        7.50        34.03         9.73         4.75         7.50
//        8.00        41.10         9.40         5.00         8.00
//
// %overflow   = percentage of buckets which have an overflow bucket
// bytes/entry = overhead bytes used per key/elem pair
// hitprobe    = # of entries to check when looking up a present key
// missprobe   = # of entries to check when looking up an absent key
//
// Keep in mind this data is for maximally loaded tables, i.e. just
// before the table grows. Typical tables will be somewhat less loaded.


/*
	扩容触发条件
	
	取样装载因子：装载因子的值过大会导致map会有大量的溢出桶，太小就会导致频繁的创建多余的内存，导致空间浪费.
	go 团队用不同的装载因子测试了下map扩容时的性能表现，如下表：
	(64位机器，键值对都是8byte大小的测试环境)
        转载因子    溢出桶占比(%)   字节/条目         命中率        失误率
          4.00         2.13        20.77         3.00         4.00
          4.50         4.05        17.30         3.25         4.50
          5.00         6.85        14.77         3.50         5.00
          5.50        10.55        12.94         3.75         5.50
          6.00        15.27        11.67         4.00         6.00
          6.50        20.90        10.79         4.25         6.50
          7.00        27.14        10.15         4.50         7.00
          7.50        34.03         9.73         4.75         7.50
          8.00        41.10         9.40         5.00         8.00
          
	溢出桶占比(%) = 每个数组桶中溢出桶的占比
	字节/条目 = 平均一个键值对占用的字节大小
	命中率 = key存在map里面时，平均需要查找几个key才找到
	失误率 = key不存在map里面时，平均需要查找几个key才推出
	
*/

import (
	"internal/abi"
	"internal/goarch"
	"runtime/internal/atomic"
	"runtime/internal/math"
	"unsafe"
)

const (
	// Maximum number of key/elem pairs a bucket can hold.
    // (每个桶可以持有的最大键值对的数量)
	bucketCntBits = abi.MapBucketCountBits // abi包的MapBucketCountBits值为3， 2的3次方=8
	bucketCnt     = abi.MapBucketCount  // 1 <<  abi.MapBucketCountBits, 即2的3次方 = 8

	// Maximum average load of a bucket that triggers growth is bucketCnt*13/16 (about 80% full)
	// Because of minimum alignment rules, bucketCnt is known to be at least 8.
	// Represent as loadFactorNum/loadFactorDen, to allow integer math.
    
    /*
    触发桶扩容的最大平均负载值为：bucketCnt*13/16 （大约桶数据填充有80%）
    由于最小对齐原则（内存对齐），bucketCnt至少是8.
    
    负载因子的值由 loadFactorNum/loadFactorDen 取整得出.
    */
	loadFactorDen = 2
	loadFactorNum = (bucketCnt * 13 / 16) * loadFactorDen

	// Maximum key or elem size to keep inline (instead of mallocing per element).
	// Must fit in a uint8.
	// Fast versions cannot handle big elems - the cutoff size for
	// fast versions in cmd/compile/internal/gc/walk.go must be at most this elem.
    
    /*
    键值对的最大字节定义为常量128字节（替代为每个键值对申请内存的做法），uint8类型
    快速迭代版本不能处理大字节的键值对 - 下面定义了最大字节大小
    快速迭代版本 在 cmd/compile/internal/gc/walk.go 目录文件的代码，最大大小就是这类元素 
    （TODO：walk.go 在1.17版本后就不存在了，即此处可以提交issue 给go官方）。
    */
	maxKeySize  = abi.MapMaxKeyBytes // 128
	maxElemSize = abi.MapMaxElemBytes  // 128

	// data offset should be the size of the bmap struct, but needs to be
	// aligned correctly. For amd64p32 this means 64-bit alignment
	// even though pointers are 32 bit.
    
    /*
    数据偏移时应为bmap结构体的倍数，并且保持内存正确的对齐.
    例如， amd64p32 代表 即使是32位的指针仍然保持64位的内存对齐.
    这样方便后面做统一的指针偏移计算
    */
	dataOffset = unsafe.Offsetof(struct {
		b bmap
		v int64
	}{}.v)  // unsafe.Offsetof 会返回从这个结构体开始的位置到结构体变量v开始的位置的偏移量 （uintptr类型的number）

	// Possible tophash values. We reserve a few possibilities for special marks.
	// Each bucket (including its overflow buckets, if any) will have either all or none of its
	// entries in the evacuated* states (except during the evacuate() method, which only happens
	// during map writes and thus no one else can observe the map during that time).
    
    /*
    tophash的值：下方是tophash 的一些特殊状态标记.
    每一个桶（包含溢出桶）在迁移状态下（evacuated开头的状态），拥有当前桶的全部数据。（除了正在执行evacuate()方法的情况，这只发生在map写入的情况下，因为此时没有人能够观察到map的状态）。
    注：桶的单位为cell，存放键值对
    */
	emptyRest      = 0 // this cell is empty, and there are no more non-empty cells at higher indexes or overflows.
    				   // 当前单元格为空，并且比它高索引位的cell或者overflows中的cell都是空的.
	emptyOne       = 1 // this cell is empty
    				   // 当前单元格为空.
	evacuatedX     = 2 // key/elem is valid.  Entry has been evacuated to first half of larger table.
    				   // key/elem 是有效的. 数据已被迁移到更大的哈希表的前半部分
	evacuatedY     = 3 // same as above, but evacuated to second half of larger table.
    				   // 同上，但是数据是被迁移到更大的哈希表的后半部分
	evacuatedEmpty = 4 // cell is empty, bucket is evacuated.
    				   // 单元格为空，数据已被迁移（扩容）.
	minTopHash     = 5 // minimum tophash for a normal filled cell.
    				   // tophash被正常填充的最小值.

	// flags 标识
	iterator     = 1 // there may be an iterator using buckets 
    				 // 迭代器正在使用桶
	oldIterator  = 2 // there may be an iterator using oldbuckets
    				 // 迭代器正在使用旧桶
	hashWriting  = 4 // a goroutine is writing to the map
    				 // 一个正在等待写入数据到map的携程
	sameSizeGrow = 8 // the current map growth is to a new map of the same size
    				 // map扩容的大小

	// sentinel bucket ID for iterator checks
    // 迭代检查用到的哨兵桶ID
	noCheck = 1<<(8*goarch.PtrSize) - 1
)

// isEmpty reports whether the given tophash array entry represents an empty bucket entry.
// isEmpty 方法用 tophash数组代表的第i个元素作为参数与emptyOne进行比较，代表当前是都是空桶。
func isEmpty(x uint8) bool {
	return x <= emptyOne
}

// A header for a Go map.
type hmap struct {
	// Note: the format of the hmap is also encoded in cmd/compile/internal/reflectdata/reflect.go.
	// Make sure this stays in sync with the compiler's definition.
    /*
    注意：hmap的结构在 cmd/compile/internal/reflectdata/reflect.go 反射的源码中被使用了.
    确保两边的结构是一致的.
    */
	count     int // # live cells == size of map.  Must be first (used by len() builtin)
			      // # 这里代表的是map的大小，必须处于第一个位置(用于内置方法 len())
	flags     uint8
	B         uint8  // log_2 of # of buckets (can hold up to loadFactor * 2^B items)
    				 // log以2为底的桶数量（可以保存 loadFactor * 2^B 的条目）
	noverflow uint16 // approximate number of overflow buckets; see incrnoverflow for details
    				 // 溢出桶的近似数量；具体参考 incrnoverflow
	hash0     uint32 // hash seed
    				 // 哈希种子

	buckets    unsafe.Pointer // array of 2^B Buckets. may be nil if count==0.
    						  // 2^B 的桶数组. 当count==0时，值为nil.
	oldbuckets unsafe.Pointer // previous bucket array of half the size, non-nil only when growing
    						  // 当扩容时，oldbuckets不为空，且大小是扩容之后的一半.
	nevacuate  uintptr        // progress counter for evacuation (buckets less than this have been evacuated)
    						  // 扩容进度 （当桶的地址小于此时，代表桶已被迁移，类似index）.

	extra *mapextra // optional fields
    				// 可选字段。为了优化GC扫描而设计的。当key和value均不包含指针，并且都可以inline时使用。extra是指向mapextra类型的指针
}

// mapextra holds fields that are not present on all maps.
// mapextra 的字段不一定包含在所有的map中.
type mapextra struct {
	// If both key and elem do not contain pointers and are inline, then we mark bucket
	// type as containing no pointers. This avoids scanning such maps.
	// However, bmap.overflow is a pointer. In order to keep overflow buckets
	// alive, we store pointers to all overflow buckets in hmap.extra.overflow and hmap.extra.oldoverflow.
	// overflow and oldoverflow are only used if key and elem do not contain pointers.
	// overflow contains overflow buckets for hmap.buckets.
	// oldoverflow contains overflow buckets for hmap.oldbuckets.
	// The indirection allows to store a pointer to the slice in hiter.
    /*
      当key和value均不包含指针，并且都可以inline时，标记桶为非指针类型.避免了gc扫描map，消耗性能.
      然而，bmap.overflow 是指针. 
      为了保证 overflow 存活,缓存所有溢出桶指针在hmap.extra.overflow 和 hmap.extra.oldoverflow.
      overflow 和 oldoverflow 仅被用于当键值对不包含指针的场景.
      overflow 包含 hmap.buckets 的溢出桶.
      oldoverflow 包含 hmap.oldbuckets的溢出桶.
      为了提高切边的命中率，间接允许存储指针.
    */
	overflow    *[]*bmap
	oldoverflow *[]*bmap

	// nextOverflow holds a pointer to a free overflow bucket.
    // nextOverflow 保持连接一个空闲的溢出桶.
	nextOverflow *bmap
}

// A bucket for a Go map.
// map的一个桶的结构.
type bmap struct {
	// tophash generally contains the top byte of the hash value
	// for each key in this bucket. If tophash[0] < minTopHash,
	// tophash[0] is a bucket evacuation state instead.
    /*
      每个桶中的每个key的tophash 一般包含高位的hash值来定位key的位置.
      如果 tophash[0] < minTopHash, 此时 tophash[0] 表示的是桶的迁移状态.
    */
	tophash [bucketCnt]uint8
	// Followed by bucketCnt keys and then bucketCnt elems.
	// NOTE: packing all the keys together and then all the elems together makes the
	// code a bit more complicated than alternating key/elem/key/elem/... but it allows
	// us to eliminate padding which would be needed for, e.g., map[int64]int8.
	// Followed by an overflow pointer.
    /*
     bucketCnt keys 和 bucketCnt elems 是紧挨着的（TODO不理解）.
     注意：所有的key是存储在一起的，所有的value也是存储在一起的，存储方式比 key/elem/key/elem/ 的形式复杂一些。
     但是允许不去判定例如 map[int64]int8 类型的值的内存填充是否对齐.
     后面跟着溢出指针.
    */
}

// A hash iteration structure.
// If you modify hiter, also change cmd/compile/internal/reflectdata/reflect.go
// and reflect/value.go to match the layout of this structure.
/*
hash表迭代器的结构体定义.
如果修改hiter，请同步修改 cmd/compile/internal/reflectdata/reflect.go.
reflect/value.go 里面的hiter反射定义的字段结构需要与这里保持一致.
*/
type hiter struct {
	key         unsafe.Pointer // Must be in first position.  Write nil to indicate iteration end (see cmd/compile/internal/walk/range.go).
    						   // 必须在第一个位置定义. 为nil时，表示迭代完成（详情查看cmd/compile/internal/walk/range.go）
	elem        unsafe.Pointer // Must be in second position (see cmd/compile/internal/walk/range.go)
    						   // 必须在第二个位置定义
    //      range.go 取key,value
    // 		keysym := th.Field(0).Sym
	//  	elemsym := th.Field(1).Sym // ditto
    
	t           *maptype      // 存放map的key，value，桶的类型和大小
	h           *hmap
	buckets     unsafe.Pointer // bucket ptr at hash_iter initialization time
    						   // 桶指针在迭代时初始化
	bptr        *bmap          // current bucket
    						   // 指向当前桶
	overflow    *[]*bmap       // keeps overflow buckets of hmap.buckets alive
    						   // 保持hmap.buckets溢出桶的生命周期
	oldoverflow *[]*bmap       // keeps overflow buckets of hmap.oldbuckets alive
    						   // 保持hmap.oldbuckets溢出桶的生命周期
	startBucket uintptr        // bucket iteration started at
    						   // 桶迭代开始的位置
	offset      uint8          // intra-bucket offset to start from during iteration (should be big enough to hold bucketCnt-1)
    						   // 在迭代期间，桶内地址偏移量的开始位置（必须足够大，能够容纳一个桶的大小）
	wrapped     bool           // already wrapped around from end of bucket array to beginning
    						   // 已经从存储桶数组的末尾迭代到开头环绕
	B           uint8
	i           uint8
	bucket      uintptr
	checkBucket uintptr
}

// bucketShift returns 1<<b, optimized for code generation.
// 桶位移 1 << b运算， 为了优化代码生成
func bucketShift(b uint8) uintptr {
	// Masking the shift amount allows overflow checks to be elided.
    // 位运算可以省略溢出检查
	return uintptr(1) << (b & (goarch.PtrSize*8 - 1))
}

// bucketMask returns 1<<b - 1, optimized for code generation.
// hmap.B的类型为uint8，最大值为255，但golang中bucket的最大限制为pow(2,64)-1,具体源码如下，在使用时会对b做64位防溢出处理
func bucketMask(b uint8) uintptr {
	return bucketShift(b) - 1
}

// tophash calculates the tophash value for hash.
// tophash 由 hash位移运算而来
func tophash(hash uintptr) uint8 {
	top := uint8(hash >> (goarch.PtrSize*8 - 8)) // hash （右移8个指针单元 减去 8 ）位，即取高N位.
	if top < minTopHash { // 如果低于minTopHash=5，则加上minTopHash
		top += minTopHash
	}
	return top
}

// 判断是否已搬迁（扩容）
func evacuated(b *bmap) bool {
	h := b.tophash[0] // 取搬迁状态
	return h > emptyOne && h < minTopHash // h > emptyOne=1（当前单元格为空） 且   h < minTopHash=5（小于tophash的最小值），都符合条件，代表已扩容.
}

// 溢出桶的位置（tophash），通过当前桶的size进行指针内存运算，得到溢出桶的位置.
func (b *bmap) overflow(t *maptype) *bmap {
	return *(**bmap)(add(unsafe.Pointer(b), uintptr(t.BucketSize)-goarch.PtrSize))
}

// 当前溢出桶的指针指向ovf
func (b *bmap) setoverflow(t *maptype, ovf *bmap) {
	*(**bmap)(add(unsafe.Pointer(b), uintptr(t.BucketSize)-goarch.PtrSize)) = ovf
}

// 位运算得到当前桶的keys开始的位置.
func (b *bmap) keys() unsafe.Pointer {
	return add(unsafe.Pointer(b), dataOffset)
}

// incrnoverflow increments h.noverflow.
// noverflow counts the number of overflow buckets.
// This is used to trigger same-size map growth.
// See also tooManyOverflowBuckets.
// To keep hmap small, noverflow is a uint16.
// When there are few buckets, noverflow is an exact count.
// When there are many buckets, noverflow is an approximate count.
/*
	incrnoverflow方法目的是增长h.noverflow.
	noverflow = 溢出桶的数量
	该值被用作触发map扩容.
	在 tooManyOverflowBuckets 也被用到.
	为了保持hmap不要太大，noverflow被定义位 uint16.
	
	当桶数量较少时，noverflow是一个准确的值。
	当桶数量较多时，noverflow是一个近似的值。
*/
func (h *hmap) incrnoverflow() {
	// We trigger same-size map growth if there are
	// as many overflow buckets as buckets.
	// We need to be able to count to 1<<h.B.
    // 等量扩容的触发机制：原桶的数量 = 溢出桶的数量
    // noverflow 此时为 1<<h.B
	if h.B < 16 {
		h.noverflow++ // 当 h.B < 16 时， noverflow++
		return
	}
	// Increment with probability 1/(1<<(h.B-15)).
	// When we reach 1<<15 - 1, we will have approximately
	// as many overflow buckets as buckets.
    /*
    noverflow以 1/(1<<(h.B-15))的概率递增.
    当h.B = noverflow = 1<<15 - 1，我们就认为溢出桶 约等于 原桶.
    */
	mask := uint32(1)<<(h.B-15) - 1
	// Example: if h.B == 18, then mask == 7,
	// and fastrand & 7 == 0 with probability 1/8.
    // 例如： 如果 h.B == 18, 那么 mask == 7,
    // fastrand & 7 == 0 ， 代表概率为 1/8
	if fastrand()&mask == 0 {  // （TODO 随机数与mask对于就可判断溢出桶数量要增加？）
		h.noverflow++
	}
}

// 创建一个溢出链表
func (h *hmap) newoverflow(t *maptype, b *bmap) *bmap {
	var ovf *bmap
    // 当有预分配的溢出桶，则直接使用，没有则内存new一个。
	if h.extra != nil && h.extra.nextOverflow != nil {
		// We have preallocated overflow buckets available.
		// See makeBucketArray for more details.
        // 在 makeBucketArray 中，我们都会预分配一个 overflow
		ovf = h.extra.nextOverflow
		if ovf.overflow(t) == nil {
			// We're not at the end of the preallocated overflow buckets. Bump the pointer.
            // 有溢出桶的数据，说明不是最后一个溢出桶，直接指向下一个溢出桶的位置
			h.extra.nextOverflow = (*bmap)(add(unsafe.Pointer(ovf), uintptr(t.BucketSize)))
		} else {
			// This is the last preallocated overflow bucket.
			// Reset the overflow pointer on this bucket,
			// which was set to a non-nil sentinel value.
            // 最后一个溢出桶，需要重置溢出指针（该指针已设置为非nil标记值），nextOverflow = nil
			ovf.setoverflow(t, nil)
			h.extra.nextOverflow = nil
		}
	} else {
		ovf = (*bmap)(newobject(t.Bucket))
	}
	h.incrnoverflow() // 溢出桶数量增加
	if t.Bucket.PtrBytes == 0 { // 如果桶中不含指针类型的key/value
		h.createOverflow() // 真正申请内存的地方
		*h.extra.overflow = append(*h.extra.overflow, ovf) //溢出链表后追加一个
	}
	b.setoverflow(t, ovf) // 指针指向ovf的位置
	return ovf
}

func (h *hmap) createOverflow() { // 申请溢出桶的内存
	if h.extra == nil {
		h.extra = new(mapextra)
	}
	if h.extra.overflow == nil {
		h.extra.overflow = new([]*bmap)
	}
}

// // 创建map内存，make(map[k]v, hint) ， hint为 int64类型时，会先检查是否超过int类型，超过则不分配，则代表map长度不能超过int的范围.
func makemap64(t *maptype, hint int64, h *hmap) *hmap { 
	if int64(int(hint)) != hint {
		hint = 0
	}
	return makemap(t, int(hint), h)
}

// makemap_small implements Go map creation for make(map[k]v) and
// make(map[k]v, hint) when hint is known to be at most bucketCnt
// at compile time and the map needs to be allocated on the heap.
/*
  makemap_small实现： 当 make(map[k]v, hint) 的hint过大时，编译期间取药在堆上申请内存.
*/
func makemap_small() *hmap {
	h := new(hmap)
	h.hash0 = fastrand()
	return h
}

// makemap implements Go map creation for make(map[k]v, hint).
// If the compiler has determined that the map or the first bucket
// can be created on the stack, h and/or bucket may be non-nil.
// If h != nil, the map can be created directly in h.
// If h.buckets != nil, bucket pointed to can be used as the first bucket.
/*
  makemap : 创建map内存， 语法为：make(map[k]v, hint).
  编译器确定map或者第一个桶可以被创建在栈上， h 和 桶 可能不为nil.
  如果 h != nil, map=hmap，直接创建.
  If h.buckets != nil, bucket被用作指向第一个桶.
*/
func makemap(t *maptype, hint int, h *hmap) *hmap {
    // MulUintptr 返回 hint*t.Bucket.Size_ = mem, 若超过最大MaxUintptr， overflow 则为true.
    // 溢出或者超过，则hint为0.
	mem, overflow := math.MulUintptr(uintptr(hint), t.Bucket.Size_)
	if overflow || mem > maxAlloc {
		hint = 0
	}

	// initialize Hmap
	if h == nil {
		h = new(hmap)
	}
	h.hash0 = fastrand() // hash种子

	// Find the size parameter B which will hold the requested # of elements.
	// For hint < 0 overLoadFactor returns false since hint < bucketCnt.
    /*
    
    func overLoadFactor(count int, B uint8) bool {
    	// hint > B 且 hint 大于 loadFactorNum*(bucketShift(B)/loadFactorDen
		return count > bucketCnt && uintptr(count) > loadFactorNum*(bucketShift(B)/loadFactorDen)
	}
	满足此条件， 则桶数量++
    */
	B := uint8(0)
	for overLoadFactor(hint, B) { // hint 为 0 是，为false， B = 0
		B++
	}
	h.B = B

	// allocate initial hash table
	// if B == 0, the buckets field is allocated lazily later (in mapassign)
	// If hint is large zeroing this memory could take a while.
    /*
    	初始化hash表
    	如果 B == 0, 桶将在mapassign被初始化，即不会提前预分配地址（因为没指定长度，或者指定的太大）;
    	如果 hint引物内存过大导致为0,申请内存需要花费一些时间.
    */
	if h.B != 0 {
		var nextOverflow *bmap
		h.buckets, nextOverflow = makeBucketArray(t, h.B, nil)
		if nextOverflow != nil {
			h.extra = new(mapextra)
			h.extra.nextOverflow = nextOverflow
		}
	}

	return h
}

// makeBucketArray initializes a backing array for map buckets.
// 1<<b is the minimum number of buckets to allocate.
// dirtyalloc should either be nil or a bucket array previously
// allocated by makeBucketArray with the same t and b parameters.
// If dirtyalloc is nil a new backing array will be alloced and
// otherwise dirtyalloc will be cleared and reused as backing array.
/*
  makeBucketArray 初始化map的数组桶.
   桶的最小初始化数量为 1<<b.
   dirtyalloc 必须是 nil 否者 桶数组通过 makeBucketArray的参数 t 和 b 预先分配.
   如果 dirtyalloc = nil, 会预分配一个数组桶,否则 dirtyalloc 会被清空 并被用于数组桶.
*/
func makeBucketArray(t *maptype, b uint8, dirtyalloc unsafe.Pointer) (buckets unsafe.Pointer, nextOverflow *bmap) {
	base := bucketShift(b) // 即 1<<b
	nbuckets := base
	// For small b, overflow buckets are unlikely.
	// Avoid the overhead of the calculation.
    // 对于 b < 4 的， 溢出桶不存在. 不会执行下面代码块的逻辑，避免额外的计算开销.
	if b >= 4 {
		// Add on the estimated number of overflow buckets
		// required to insert the median number of elements
		// used with this value of b.
        
   		// 要求通过b值算出数据的平均数 预估偏移的溢出桶内存
		nbuckets += bucketShift(b - 4)
		sz := t.Bucket.Size_ * nbuckets
		up := roundupsize(sz) // 申请sz大小的内存
		if up != sz {
			nbuckets = up / t.Bucket.Size_
		}
	}

	if dirtyalloc == nil {
		buckets = newarray(t.Bucket, int(nbuckets))
	} else {
		// dirtyalloc was previously generated by
		// the above newarray(t.Bucket, int(nbuckets))
		// but may not be empty.
        // dirtyalloc 是通过上面的  newarray(t.Bucket, int(nbuckets)) 生成的，不可能会empty.
        // 下面说明桶已经被创建，需要做清除
		buckets = dirtyalloc
		size := t.Bucket.Size_ * nbuckets
		if t.Bucket.PtrBytes != 0 {
			memclrHasPointers(buckets, size)
		} else {
			memclrNoHeapPointers(buckets, size)
		}
	}

	if base != nbuckets {
		// We preallocated some overflow buckets.
		// To keep the overhead of tracking these overflow buckets to a minimum,
		// we use the convention that if a preallocated overflow bucket's overflow
		// pointer is nil, then there are more available by bumping the pointer.
		// We need a safe non-nil pointer for the last overflow bucket; just use buckets.
        /*
        	预分配相同大小的溢出桶.
        	为了保证溢出桶计算的最小开销，如果预配置溢出桶的 overflow 指针为nil 使用地址转换,
        	然后通过指针“碰撞”会有更多可以的.
        	需要使用不为nil的指针作为最后一个溢出桶；使用 buckets 即可.
        */
		nextOverflow = (*bmap)(add(buckets, base*uintptr(t.BucketSize))) // 溢出桶位置：buckets + base*BucketSize
        last := (*bmap)(add(buckets, (nbuckets-1)*uintptr(t.BucketSize))) // 最后一个位置:buckets+ (nbuckets-1)*BucketSize
		last.setoverflow(t, (*bmap)(buckets)) // 设置buckets类型的指针作为最后一个溢出桶.
	}
	return buckets, nextOverflow
}

// mapaccess1 returns a pointer to h[key].  Never returns nil, instead
// it will return a reference to the zero object for the elem type if
// the key is not in the map.
// NOTE: The returned pointer may keep the whole map live, so don't
// hold onto it for very long.
/*
	mapaccess1 返回指向 h[key]的一个指针.即根据key在map中查找。 
	
	使用方式 ： value := map[key]
	
	即使key在map里面不存在也不会返回nil，而是会返回一个可以被访问的空对象.
	注意: 返回的指针的生命周期在map的生命周期范围内，不要持有它的时间太长.
*/
func mapaccess1(t *maptype, h *hmap, key unsafe.Pointer) unsafe.Pointer {
	if raceenabled && h != nil { // 有竞态，且h不为nil。 raceenabled ：定义在runtime包中。它用于启用数据竞争检查功能，让编译器和运行时监视器能检测到并报告潜在的危险行为
		callerpc := getcallerpc() // 获取程序计数寄存器指针
		pc := abi.FuncPCABIInternal(mapaccess1) // 取 mapaccess1的寄存器地址
		racereadpc(unsafe.Pointer(h), callerpc, pc) 
        // 变量raceenabled用于判断 race detector 是否被启用。若被启用，则调用racereadpc函数，该函数的作用是向race detector注册读取操作，
        // 并记录 mapaccess1 函数的调用信息和调用位置。这样，race detector就可以通过跟踪这些信息，识别并报告潜在的并发竞争问题
        
		raceReadObjectPC(t.Key, key, callerpc, pc) // 竟态检查
	}
   
    // ASan 无法覆盖到未初始化的内存，对于未初始化的内存，进行读取的行为同样危险，这时候就需要 MSan 出马了。
	// MSan 实现的是一个 bit to bit 的影子内存，每一比特都有映射，所以在计算影子内存的位置时，十分高效。
    
	if msanenabled && h != nil {
		msanread(key, t.Key.Size_)
	}
    
     // ASan 是用来检测 释放后使用(use-after-free)、多次释放(double-free)、缓冲区溢出(buffer overflows)和下溢(underflows) 的内存问题
    // 参考: https://zhuanlan.zhihu.com/p/390555316
	if asanenabled && h != nil {
		asanread(key, t.Key.Size_)
	}
	if h == nil || h.count == 0 { //当h为空或者长度为0时，直接返回空map
		if t.HashMightPanic() {
			t.Hasher(key, 0) // see issue 23734 
            /* 
            
            bug号 23734： https://github.com/golang/go/issues/23735
            // go 1.11修复了此问题
            
            var m = map[interface{}]int{0: 0}  // non-empty map to workaround #23734
            var k []int
            var x int

            m[k] /= x
            
            */
            
		}
		return unsafe.Pointer(&zeroVal[0])
	}
    
	if h.flags&hashWriting != 0 { //此时map正在进行数据写入，直接panic
		fatal("concurrent map read and map write")
	}
    // 计算key的hash值，并与map的桶的地址进行运算得到，key所在的数组桶的地址。
	hash := t.Hasher(key, uintptr(h.hash0))
	m := bucketMask(h.B)
	b := (*bmap)(add(h.buckets, (hash&m)*uintptr(t.BucketSize)))
	if c := h.oldbuckets; c != nil {
		if !h.sameSizeGrow() {
			// There used to be half as many buckets; mask down one more power of two.
            // 旧桶不为nil说明扩容了，又判断出不是等量扩容，所以桶的数量直接乘以原来的2倍.
			m >>= 1
		}
        // 由于扩容了，需要先判断扩容是否结束，若结束，则计算key在扩容后的桶的位置，否则还是旧桶的位置.
		oldb := (*bmap)(add(c, (hash&m)*uintptr(t.BucketSize)))
		if !evacuated(oldb) {
			b = oldb
		}
	}
    // 取key的高位
	top := tophash(hash)
bucketloop: // go的类似goto的loop语法
	for ; b != nil; b = b.overflow(t) { // 遍历桶
		for i := uintptr(0); i < bucketCnt; i++ { // 遍历元素
			if b.tophash[i] != top { // 没找到
				if b.tophash[i] == emptyRest { // 且此时桶的cell都是空值，以及后面的桶都是空值,不用查了，说明没查到，推出循环.
					break bucketloop
				}
				continue
			}
			k := add(unsafe.Pointer(b), dataOffset+i*uintptr(t.KeySize)) // 查到key值得hash对应的桶，取出key = b+bmap+i*size
			if t.IndirectKey() { // key如果是指针
				k = *((*unsafe.Pointer)(k)) // 取真正的值
			}
			if t.Key.Equal(key, k) { // 比较值是否相等
                // 查找value的值的 =  b+bmap+key的总偏移+i*size.
				e := add(unsafe.Pointer(b), dataOffset+bucketCnt*uintptr(t.KeySize)+i*uintptr(t.ValueSize))
				if t.IndirectElem() { 
					e = *((*unsafe.Pointer)(e))
				}
				return e
			}
		}
	}
	return unsafe.Pointer(&zeroVal[0]) // 查不到返回默认值
}

/*
	使用方式 ： value, ok := map[key]
	逻辑和上面的基本一致，只是多了bool的返回告知使用者，key值是否存在.
*/
func mapaccess2(t *maptype, h *hmap, key unsafe.Pointer) (unsafe.Pointer, bool) {
	if raceenabled && h != nil {
		callerpc := getcallerpc()
		pc := abi.FuncPCABIInternal(mapaccess2)
		racereadpc(unsafe.Pointer(h), callerpc, pc)
		raceReadObjectPC(t.Key, key, callerpc, pc)
	}
	if msanenabled && h != nil {
		msanread(key, t.Key.Size_)
	}
	if asanenabled && h != nil {
		asanread(key, t.Key.Size_)
	}
	if h == nil || h.count == 0 {
		if t.HashMightPanic() {
			t.Hasher(key, 0) // see issue 23734
		}
		return unsafe.Pointer(&zeroVal[0]), false
	}
	if h.flags&hashWriting != 0 {
		fatal("concurrent map read and map write")
	}
	hash := t.Hasher(key, uintptr(h.hash0))
	m := bucketMask(h.B)
	b := (*bmap)(add(h.buckets, (hash&m)*uintptr(t.BucketSize)))
	if c := h.oldbuckets; c != nil {
		if !h.sameSizeGrow() {
			// There used to be half as many buckets; mask down one more power of two.
			m >>= 1
		}
		oldb := (*bmap)(add(c, (hash&m)*uintptr(t.BucketSize)))
		if !evacuated(oldb) {
			b = oldb
		}
	}
	top := tophash(hash)
bucketloop:
	for ; b != nil; b = b.overflow(t) {
		for i := uintptr(0); i < bucketCnt; i++ {
			if b.tophash[i] != top {
				if b.tophash[i] == emptyRest {
					break bucketloop
				}
				continue
			}
			k := add(unsafe.Pointer(b), dataOffset+i*uintptr(t.KeySize))
			if t.IndirectKey() {
				k = *((*unsafe.Pointer)(k))
			}
			if t.Key.Equal(key, k) {
				e := add(unsafe.Pointer(b), dataOffset+bucketCnt*uintptr(t.KeySize)+i*uintptr(t.ValueSize))
				if t.IndirectElem() {
					e = *((*unsafe.Pointer)(e))
				}
				return e, true  // 这里查到返回true
			}
		}
	}
	return unsafe.Pointer(&zeroVal[0]), false  // 没查到返回false.
}

// returns both key and elem. Used by map iterator.
// 根据key，返回key,value, 用于迭代场景
// for key, value := range map
func mapaccessK(t *maptype, h *hmap, key unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer) {
	if h == nil || h.count == 0 {
		return nil, nil
	}
	hash := t.Hasher(key, uintptr(h.hash0))
	m := bucketMask(h.B)
	b := (*bmap)(add(h.buckets, (hash&m)*uintptr(t.BucketSize)))
	if c := h.oldbuckets; c != nil {
		if !h.sameSizeGrow() {
			// There used to be half as many buckets; mask down one more power of two.
			m >>= 1
		}
		oldb := (*bmap)(add(c, (hash&m)*uintptr(t.BucketSize)))
		if !evacuated(oldb) {
			b = oldb
		}
	}
	top := tophash(hash)
bucketloop:
	for ; b != nil; b = b.overflow(t) {
		for i := uintptr(0); i < bucketCnt; i++ {
			if b.tophash[i] != top {
				if b.tophash[i] == emptyRest {
					break bucketloop
				}
				continue
			}
			k := add(unsafe.Pointer(b), dataOffset+i*uintptr(t.KeySize))
			if t.IndirectKey() {
				k = *((*unsafe.Pointer)(k))
			}
			if t.Key.Equal(key, k) {
				e := add(unsafe.Pointer(b), dataOffset+bucketCnt*uintptr(t.KeySize)+i*uintptr(t.ValueSize))
				if t.IndirectElem() {
					e = *((*unsafe.Pointer)(e))
				}
				return k, e // 直接返回key,value
			}
		}
	}
	return nil, nil // 直接返回nil，nil，一般是map为空或者遍历结束了
}

/*
			编译器编译行为：  cmd/compile/internal/walk/assign.go
			const zeroValSize = 1024
			
			if w := t.Elem().Width; w <= zeroValSize {
				n = mkcall1(mapfn(mapaccess1[fast], t), types.NewPtr(t.Elem()), init, typename(t), map_, key)
			} else {
				z := zeroaddr(w)
				n = mkcall1(mapfn("mapaccess1_fat", t), types.NewPtr(t.Elem()), init, typename(t), map_, key, z)
			}
			
			cell存储的元素大小大于1024个字节时，使用下面的方式查找map

*/
func mapaccess1_fat(t *maptype, h *hmap, key, zero unsafe.Pointer) unsafe.Pointer {
	e := mapaccess1(t, h, key)
	if e == unsafe.Pointer(&zeroVal[0]) { // 查不到，返回zero
		return zero
	}
	return e
}

func mapaccess2_fat(t *maptype, h *hmap, key, zero unsafe.Pointer) (unsafe.Pointer, bool) {
	e := mapaccess1(t, h, key)
	if e == unsafe.Pointer(&zeroVal[0]) {
		return zero, false
	}
	return e, true
}

// Like mapaccess, but allocates a slot for the key if it is not present in the map.
// mapassign， 使用： map[k] = value ， 区别于 mapaccess， 查到更新，查不到就赋值.
func mapassign(t *maptype, h *hmap, key unsafe.Pointer) unsafe.Pointer {
	if h == nil {
        panic(plainError("assignment to entry in nil map")) // panic： nil map禁止直接赋值， 即必须 map:= make(map[T]T) 才可赋值
	}
	if raceenabled {
		callerpc := getcallerpc()
		pc := abi.FuncPCABIInternal(mapassign)
		racewritepc(unsafe.Pointer(h), callerpc, pc)
		raceReadObjectPC(t.Key, key, callerpc, pc)
	}
	if msanenabled {
		msanread(key, t.Key.Size_)
	}
	if asanenabled {
		asanread(key, t.Key.Size_)
	}
	if h.flags&hashWriting != 0 {
		fatal("concurrent map writes")
	}
	hash := t.Hasher(key, uintptr(h.hash0))

	// Set hashWriting after calling t.hasher, since t.hasher may panic,
	// in which case we have not actually done a write.
    // 使用 t.hasher 后，将 map 的状态设置为正在写，此后对map并发读写都会导致panic.
    // 这种情况，表示map正在写入.
	h.flags ^= hashWriting

	if h.buckets == nil { // 桶为空，则创建
		h.buckets = newobject(t.Bucket) // newarray(t.Bucket, 1)
	}

again: // goto语法，禁止开发使用，导致代码结构混乱，难以维护
	bucket := hash & bucketMask(h.B)
	if h.growing() { // 是否正在扩容
		growWork(t, h, bucket) // 加速扩容
	}
    // 查找key所在的数组桶的地址
	b := (*bmap)(add(h.buckets, bucket*uintptr(t.BucketSize)))
	top := tophash(hash)

	var inserti *uint8
	var insertk unsafe.Pointer
	var elem unsafe.Pointer
bucketloop:
	for {
		for i := uintptr(0); i < bucketCnt; i++ { // 遍历数组桶
			if b.tophash[i] != top { // key的地址与桶的地址不匹配
				if isEmpty(b.tophash[i]) && inserti == nil { // 是空桶且插入标记为nil （桶是连续的，且没找到key，说明要把key插入）
					inserti = &b.tophash[i] // 标记插入位
					insertk = add(unsafe.Pointer(b), dataOffset+i*uintptr(t.KeySize)) // 标记key的桶内地址
					elem = add(unsafe.Pointer(b), dataOffset+bucketCnt*uintptr(t.KeySize)+i*uintptr(t.ValueSize)) // 标记value的桶内地址
				}
				if b.tophash[i] == emptyRest { // 如果后面的桶都是空的，没找到，直接退出loop
					break bucketloop
				}
				continue
			}
            // 查到了key的hash地址
			k := add(unsafe.Pointer(b), dataOffset+i*uintptr(t.KeySize))
			if t.IndirectKey() {
				k = *((*unsafe.Pointer)(k))
			}
			if !t.Key.Equal(key, k) { // 实际key值不相等，去下个桶找
				continue
			}
			// already have a mapping for key. Update it.
            // 找到了key，判断key是否需要覆盖（TODO : 没搞明白，key值为啥还要再更新，可能跟底层有关）
			if t.NeedKeyUpdate() {
				typedmemmove(t.Key, k, key)
			}
            // value的地址
			elem = add(unsafe.Pointer(b), dataOffset+bucketCnt*uintptr(t.KeySize)+i*uintptr(t.ValueSize))
			goto done // 跳转到done的地址.
		}
		ovf := b.overflow(t) // 查找下一个数组桶
		if ovf == nil { 
			break
		}
		b = ovf
	}

	// Did not find mapping for key. Allocate new cell & add entry.
	// If we hit the max load factor or we have too many overflow buckets,
	// and we're not already in the middle of growing, start growing.
    
    /*
    	key没找到，对map进行key value的赋值.
    	如果触发了负载因子且map还没开始扩容，或者有太多的溢出桶，
    	则进行扩容.
    */
	if !h.growing() && (overLoadFactor(h.count+1, h.B) || tooManyOverflowBuckets(h.noverflow, h.B)) {
		hashGrow(t, h)
		goto again // Growing the table invalidates everything, so try again
        		   // 扩容会导致所有的map位置无效，所以跳转到again的代码块执行
	}

	if inserti == nil { // 找到插入标记位
		// The current bucket and all the overflow buckets connected to it are full, allocate a new one.
        // 当前hash桶以及溢出桶都满了，需要创建一个新的桶
		newb := h.newoverflow(t, b)
		inserti = &newb.tophash[0] // 插入位置为新桶的第一个位置
		insertk = add(unsafe.Pointer(newb), dataOffset) // key的地址为偏移bmap个字节
		elem = add(insertk, bucketCnt*uintptr(t.KeySize)) // value的地址
	}

	// store new key/elem at insert position
    // 将key/value插入到指定位置
	if t.IndirectKey() {
		kmem := newobject(t.Key)
		*(*unsafe.Pointer)(insertk) = kmem
		insertk = kmem
	}
	if t.IndirectElem() {
		vmem := newobject(t.Elem)
		*(*unsafe.Pointer)(elem) = vmem
	}
	typedmemmove(t.Key, insertk, key)
    *inserti = top // (TODO: 为啥还要把tophash给插入位置，赋值结束了)
	h.count++ // map len++

done:
	if h.flags&hashWriting == 0 {
        fatal("concurrent map writes") // panic : map禁止并发写
	}
	h.flags &^= hashWriting // 再把状态改回非写入状态
	if t.IndirectElem() {
		elem = *((*unsafe.Pointer)(elem))
	}
	return elem // 返回value
}

// 根据key删除key/value
// 删除也要加速map的扩容
// 删除非并发安全，且删除不会返回任何结果
func mapdelete(t *maptype, h *hmap, key unsafe.Pointer) {
	if raceenabled && h != nil {
		callerpc := getcallerpc()
		pc := abi.FuncPCABIInternal(mapdelete)
		racewritepc(unsafe.Pointer(h), callerpc, pc)
		raceReadObjectPC(t.Key, key, callerpc, pc)
	}
	if msanenabled && h != nil {
		msanread(key, t.Key.Size_)
	}
	if asanenabled && h != nil {
		asanread(key, t.Key.Size_)
	}
	if h == nil || h.count == 0 {
		if t.HashMightPanic() {
			t.Hasher(key, 0) // see issue 23734
		}
		return
	}
	if h.flags&hashWriting != 0 {
		fatal("concurrent map writes")
	}

	hash := t.Hasher(key, uintptr(h.hash0))

	// Set hashWriting after calling t.hasher, since t.hasher may panic,
	// in which case we have not actually done a write (delete).
	h.flags ^= hashWriting

	bucket := hash & bucketMask(h.B)
	if h.growing() {
		growWork(t, h, bucket)
	}
	b := (*bmap)(add(h.buckets, bucket*uintptr(t.BucketSize)))
	bOrig := b
	top := tophash(hash)
search:
	for ; b != nil; b = b.overflow(t) {
		for i := uintptr(0); i < bucketCnt; i++ {
			if b.tophash[i] != top {
				if b.tophash[i] == emptyRest {
					break search
				}
				continue
			}
			k := add(unsafe.Pointer(b), dataOffset+i*uintptr(t.KeySize))
			k2 := k
			if t.IndirectKey() {
				k2 = *((*unsafe.Pointer)(k2))
			}
			if !t.Key.Equal(key, k2) {
				continue
			}
			// Only clear key if there are pointers in it.
            // 如果key/value是指针，即把key/value指针清除让gc处理即可
			if t.IndirectKey() {
				*(*unsafe.Pointer)(k) = nil
			} else if t.Key.PtrBytes != 0 {
				memclrHasPointers(k, t.Key.Size_)
			}
			e := add(unsafe.Pointer(b), dataOffset+bucketCnt*uintptr(t.KeySize)+i*uintptr(t.ValueSize))
			if t.IndirectElem() {
				*(*unsafe.Pointer)(e) = nil
			} else if t.Elem.PtrBytes != 0 {
				memclrHasPointers(e, t.Elem.Size_)
			} else {
				memclrNoHeapPointers(e, t.Elem.Size_)
			}
			b.tophash[i] = emptyOne // 清除后，将cell置为空状态
			// If the bucket now ends in a bunch of emptyOne states,
			// change those to emptyRest states.
			// It would be nice to make this a separate function, but
			// for loops are not currently inlineable.
            // 如果这个cell是桶中的最后一个，将其以及其高位空的cell都置为emptyRest状态.
			if i == bucketCnt-1 {
				if b.overflow(t) != nil && b.overflow(t).tophash[0] != emptyRest {
					goto notLast
				}
			} else {
				if b.tophash[i+1] != emptyRest {
					goto notLast
				}
			}
			for {
				b.tophash[i] = emptyRest
				if i == 0 {
					if b == bOrig {
						break // beginning of initial bucket, we're done.
					}
					// Find previous bucket, continue at its last entry.
					c := b
					for b = bOrig; b.overflow(t) != c; b = b.overflow(t) {
					}
					i = bucketCnt - 1
				} else {
					i--
				}
				if b.tophash[i] != emptyOne {
					break
				}
			}
		notLast:
			h.count--
			// Reset the hash seed to make it more difficult for attackers to
			// repeatedly trigger hash collisions. See issue 25237.
            // 当map被删除后长度为0时，将hash种子重置，重置hash冲突概率.
			if h.count == 0 {
				h.hash0 = fastrand()
			}
			break search
		}
	}

	if h.flags&hashWriting == 0 {
		fatal("concurrent map writes")
	}
	h.flags &^= hashWriting
}

// mapiterinit initializes the hiter struct used for ranging over maps.
// The hiter struct pointed to by 'it' is allocated on the stack
// by the compilers order pass or on the heap by reflect_mapiterinit.
// Both need to have zeroed hiter since the struct contains pointers.
/* 
	mapiterinit 初始化 hiter 用于遍历map.
	
	通过编译器顺序或使用堆上的reflect_mapiterinit 在栈上分配的 it 指向 hiter.
    
    因为 hiter包含指针，所以上面两种形式都需要有 空 hiter.
*/
func mapiterinit(t *maptype, h *hmap, it *hiter) {
	if raceenabled && h != nil {
		callerpc := getcallerpc()
		racereadpc(unsafe.Pointer(h), callerpc, abi.FuncPCABIInternal(mapiterinit))
	}

    it.t = t // 和 (if unsafe.Sizeof(hiter{})/goarch.PtrSize != 12 ....) 替换一下顺序更好一些
	if h == nil || h.count == 0 { 
		return
	}

	if unsafe.Sizeof(hiter{})/goarch.PtrSize != 12 { // 不满足12个指针大小的长度直接panic.
		throw("hash_iter size incorrect") 
        // see cmd/compile/internal/reflectdata/reflect.go
        /*
        		if hiter.Size() != int64(12*types.PtrSize) {
                    base.Fatalf("hash_iter size not correct %d %d", hiter.Size(), 12*types.PtrSize)
                }
                （TODO : 不明白 hiter 是12个指针大小怎么算的）
        */
	}
	it.h = h

	// grab snapshot of bucket state
    // 桶的状态的快照
	it.B = h.B
	it.buckets = h.buckets
	if t.Bucket.PtrBytes == 0 {
		// Allocate the current slice and remember pointers to both current and old.
		// This preserves all relevant overflow buckets alive even if
		// the table grows and/or overflow buckets are added to the table
		// while we are iterating.
        /*
        	创建新的数组桶，同时保持旧桶的位置.
        	当hash表发生扩容或者溢出桶增加时，方便我们快速定位到迭代的位置.
        */
		h.createOverflow()
		it.overflow = h.extra.overflow
		it.oldoverflow = h.extra.oldoverflow
	}

	// decide where to start
    // 计算从哪里开始随机迭代（map的遍历的无序性）
	var r uintptr
	if h.B > 31-bucketCntBits {
		r = uintptr(fastrand64())
	} else {
		r = uintptr(fastrand())
	}
	it.startBucket = r & bucketMask(h.B)
	it.offset = uint8(r >> h.B & (bucketCnt - 1))

	// iterator state
	it.bucket = it.startBucket

	// Remember we have an iterator.
	// Can run concurrently with another mapiterinit().
    // 迭代器可以同时运行在其他 mapiterinit 里面（并发被使用）
    // （TODO: 原子异或是为了？）
	if old := h.flags; old&(iterator|oldIterator) != iterator|oldIterator {
		atomic.Or8(&h.flags, iterator|oldIterator)
	}

	mapiternext(it) // 开始遍历
}

// 遍历的过程中不允许并发写
func mapiternext(it *hiter) {
	h := it.h
	if raceenabled {
		callerpc := getcallerpc()
		racereadpc(unsafe.Pointer(h), callerpc, abi.FuncPCABIInternal(mapiternext))
	}
	if h.flags&hashWriting != 0 {
		fatal("concurrent map iteration and map write")
	}
	t := it.t
	bucket := it.bucket
	b := it.bptr
	i := it.i
	checkBucket := it.checkBucket

next:
	if b == nil { // 当前桶位置指向为nil，（map可能为空也可能遍历到末尾了）
		if bucket == it.startBucket && it.wrapped { // 已经达到遍历末尾，直接退出
			// end of iteration
			it.key = nil
			it.elem = nil
			return
		}
		if h.growing() && it.B == h.B {
			// Iterator was started in the middle of a grow, and the grow isn't done yet.
			// If the bucket we're looking at hasn't been filled in yet (i.e. the old
			// bucket hasn't been evacuated) then we need to iterate through the old
			// bucket and only return the ones that will be migrated to this bucket.
            /*
            	map在遍历时，map正在扩容，且没扩容完.
            	如果发现当前桶没有被填满（说明旧桶还没有被迁移），
            	然后我们需要用迭代器遍历旧桶并且略过旧桶中将要被迁移到这个桶的数据.
            */
			oldbucket := bucket & it.h.oldbucketmask()
			b = (*bmap)(add(h.oldbuckets, oldbucket*uintptr(t.BucketSize))) // 先指向旧桶
			if !evacuated(b) { // 旧桶是否已经搬迁
				checkBucket = bucket // 需要检查要迁移的数据
			} else {
				b = (*bmap)(add(it.buckets, bucket*uintptr(t.BucketSize))) // 指向当前桶
				checkBucket = noCheck
			}
		} else {
			b = (*bmap)(add(it.buckets, bucket*uintptr(t.BucketSize))) // 指向当前桶
			checkBucket = noCheck
		}
		bucket++ // 迭代++
		if bucket == bucketShift(it.B) { // 遍历到末尾了
			bucket = 0
			it.wrapped = true
		}
		i = 0 // cell的第一个位置
	}
	for ; i < bucketCnt; i++ {
		offi := (i + it.offset) & (bucketCnt - 1)
        // 当前cell为空或者已经数据已经被迁移,往下遍历
		if isEmpty(b.tophash[offi]) || b.tophash[offi] == evacuatedEmpty {
			// TODO: emptyRest is hard to use here, as we start iterating
			// in the middle of a bucket. It's feasible, just tricky.
            // TODO：emptyRest 很难使用在此处，所以我们从桶的中间开始迭代. 这是可行的，但是比较麻烦.
			continue
		}
        // 找到key、value的地址
		k := add(unsafe.Pointer(b), dataOffset+uintptr(offi)*uintptr(t.KeySize))
		if t.IndirectKey() {
			k = *((*unsafe.Pointer)(k))
		}
		e := add(unsafe.Pointer(b), dataOffset+bucketCnt*uintptr(t.KeySize)+uintptr(offi)*uintptr(t.ValueSize))
		if checkBucket != noCheck && !h.sameSizeGrow() { // 需要检查在旧桶的将要迁移的数据，以及非等量扩容
			// Special case: iterator was started during a grow to a larger size
			// and the grow is not done yet. We're working on a bucket whose
			// oldbucket has not been evacuated yet. Or at least, it wasn't
			// evacuated when we started the bucket. So we're iterating
			// through the oldbucket, skipping any keys that will go
			// to the other new bucket (each oldbucket expands to two
			// buckets during a grow).
            /*
            	特殊例子：map遍历发生在2倍扩容且扩容没完成.
            	此时需要遍历一个还没迁移完毕的旧桶.
            	至少，我们开始遍历这个桶时还没迁移完毕.
            	忽略将要被迁移到新桶的数据（2倍扩容=1个旧桶的数据需要2个新桶）.
            */
			if t.ReflexiveKey() || t.Key.Equal(k, k) {
				// If the item in the oldbucket is not destined for
				// the current new bucket in the iteration, skip it.
                // 如果旧桶中的数据不在新桶中，跳过这条数据.
				hash := t.Hasher(k, uintptr(h.hash0))
				if hash&bucketMask(it.B) != checkBucket {
					continue
				}
			} else {
				// Hash isn't repeatable if k != k (NaNs).  We need a
				// repeatable and randomish choice of which direction
				// to send NaNs during evacuation. We'll use the low
				// bit of tophash to decide which way NaNs go.
				// NOTE: this case is why we need two evacuate tophash
				// values, evacuatedX and evacuatedY, that differ in
				// their low bit.
                /*
                	如果 k != k (NaNs) hash值不会重复.
                	当数据正在迁移时，我们需要选择一种不重复且随机的方法发送到NaNs.
                	使用tophash的低位决定NaNs的方向.
                	注意： 这个例子说明我们需要tophash两个低位的不同位来表示 evacuatedX 和 evacuatedY.
                */
				if checkBucket>>(it.B-1) != uintptr(b.tophash[offi]&1) {
					continue
				}
			}
		}
		if (b.tophash[offi] != evacuatedX && b.tophash[offi] != evacuatedY) ||
			!(t.ReflexiveKey() || t.Key.Equal(k, k)) {
			// This is the golden data, we can return it.
			// OR
			// key!=key, so the entry can't be deleted or updated, so we can just return it.
			// That's lucky for us because when key!=key we can't look it up successfully.
            // 这是"黄金数据"即指目标数据,我们可以直接返回。
            // 或者
            // key!=key 我们无法删除或者更新，所以只能返回. 因为 key!=key 所以我们无法成功找到数据.
			it.key = k
			if t.IndirectElem() {
				e = *((*unsafe.Pointer)(e))
			}
			it.elem = e
		} else {
			// The hash table has grown since the iterator was started.
			// The golden data for this key is now somewhere else.
			// Check the current hash table for the data.
			// This code handles the case where the key
			// has been deleted, updated, or deleted and reinserted.
			// NOTE: we need to regrab the key as it has potentially been
			// updated to an equal() but not identical key (e.g. +0.0 vs -0.0).
            /*
            	迭代时，map正在扩容。
            	目标数据可能在任何位置.
            	检查当前hash表是否存在此key.
            	这种情况只发生在key已经被删除、更新、删除再插入。
            	注意：我们需要重新拿取key地址，因为key可能存在在equal()中更新，但不是相同的key (例如： +0.0 和 -0.0).
            */
			rk, re := mapaccessK(t, h, k)
			if rk == nil {
				continue // key has been deleted
                		 // key 已经被删除
			}
			it.key = rk
			it.elem = re
		}
		it.bucket = bucket
		if it.bptr != b { // avoid unnecessary write barrier; see issue 14921 
            // 避免不必要的写入屏障(gc优化，减少写屏障，即加快gc的过程), 参考 issue: 14921 
			it.bptr = b
		}
		it.i = i + 1
		it.checkBucket = checkBucket
		return
	}
	b = b.overflow(t)
	i = 0
	goto next
}

// mapclear deletes all keys from a map.
// 清除所以的map数据.
func mapclear(t *maptype, h *hmap) {
	if raceenabled && h != nil {
		callerpc := getcallerpc()
		pc := abi.FuncPCABIInternal(mapclear)
		racewritepc(unsafe.Pointer(h), callerpc, pc)
	}

	if h == nil || h.count == 0 {
		return
	}

	if h.flags&hashWriting != 0 {
		fatal("concurrent map writes")
	}

	h.flags ^= hashWriting // 标记正在写

	// Mark buckets empty, so existing iterators can be terminated, see issue #59411.
    // 标记桶为空，现存的迭代行为需要停止， 参考 issue: 59411 ，大概的意思是，针对key是NaNs无法计算的，所以导致无法删除干净.
	markBucketsEmpty := func(bucket unsafe.Pointer, mask uintptr) { // 声明一个匿名函数,遍历标记桶为emptyRest状态.
		for i := uintptr(0); i <= mask; i++ {
			b := (*bmap)(add(bucket, i*uintptr(t.BucketSize)))
			for ; b != nil; b = b.overflow(t) {
				for i := uintptr(0); i < bucketCnt; i++ {
					b.tophash[i] = emptyRest
				}
			}
		}
	}
	markBucketsEmpty(h.buckets, bucketMask(h.B))
	if oldBuckets := h.oldbuckets; oldBuckets != nil { // 如果还有喂迁移完毕的旧桶，也需要标记
		markBucketsEmpty(oldBuckets, h.oldbucketmask())
	}

	h.flags &^= sameSizeGrow
	h.oldbuckets = nil
	h.nevacuate = 0
	h.noverflow = 0
	h.count = 0

	// Reset the hash seed to make it more difficult for attackers to
	// repeatedly trigger hash collisions. See issue 25237.
	h.hash0 = fastrand()

	// Keep the mapextra allocation but clear any extra information.
    // 重新初始化mapextra{} 以清除extra数据
	if h.extra != nil {
		*h.extra = mapextra{}
	}

	// makeBucketArray clears the memory pointed to by h.buckets
	// and recovers any overflow buckets by generating them
	// as if h.buckets was newly alloced.
    // 如果h.buckets已经被新分配内存了，则 nextOverflow 需要重新指定.
	_, nextOverflow := makeBucketArray(t, h.B, h.buckets)
	if nextOverflow != nil {
		// If overflow buckets are created then h.extra
		// will have been allocated during initial bucket creation.
        // 如果溢出桶被创建，那么h.extra在初始化桶的时候被分配内存.
		h.extra.nextOverflow = nextOverflow
	}

	if h.flags&hashWriting == 0 { // 禁止并发写
		fatal("concurrent map writes")
	}
	h.flags &^= hashWriting // 解除写状态
}

// map扩容
func hashGrow(t *maptype, h *hmap) {
	// If we've hit the load factor, get bigger.
	// Otherwise, there are too many overflow buckets,
	// so keep the same number of buckets and "grow" laterally.
    // 如果触发装载因子，bigger 作为标记值.
    // 或者溢出桶太多.
    // 增加相同桶的数量进行等量扩容，扩容时机延后（为了性能）
	bigger := uint8(1)
	if !overLoadFactor(h.count+1, h.B) { // 没触发负载因子，则等量扩容
		bigger = 0
		h.flags |= sameSizeGrow
	}
	oldbuckets := h.buckets
	newbuckets, nextOverflow := makeBucketArray(t, h.B+bigger, nil) // 通过bigger=1，右移一位代表桶数量为原来的2倍.

    // （TODO: 这一顿位运算，没搞明白flags最后是啥）
	flags := h.flags &^ (iterator | oldIterator)
	if h.flags&iterator != 0 {
		flags |= oldIterator
	}
	// commit the grow (atomic wrt gc)
    // 提交 grow （原子的gc）
	h.B += bigger
	h.flags = flags
	h.oldbuckets = oldbuckets
	h.buckets = newbuckets
	h.nevacuate = 0
	h.noverflow = 0

	if h.extra != nil && h.extra.overflow != nil {
		// Promote current overflow buckets to the old generation.
        // 将溢出桶放到旧桶中
		if h.extra.oldoverflow != nil {
			throw("oldoverflow is not nil") // 旧桶不为nil，panic
		}
		h.extra.oldoverflow = h.extra.overflow // 溢出桶给旧桶，溢出桶赋值为nil.
		h.extra.overflow = nil
	}
    
    // nextOverflow 赋值给 h.extra.nextOverflow
	if nextOverflow != nil {
		if h.extra == nil {
			h.extra = new(mapextra)
		}
		h.extra.nextOverflow = nextOverflow
	}

	// the actual copying of the hash table data is done incrementally
	// by growWork() and evacuate().
    
    // 实际通过 growWork() 和 evacuate() 方法 来完成 hash表数据的迁移.
}

// overLoadFactor reports whether count items placed in 1<<B buckets is over loadFactor.
// overLoadFactor 判断是否触发了装载因子.
func overLoadFactor(count int, B uint8) bool {
    // count > bucketCnt 桶的数量超过8 （刚开始初始化的小map）
    // uintptr(count) > loadFactorNum*(bucketShift(B)/loadFactorDen)  桶的数量超过 loadFactorNum*(bucketShift(B)/loadFactorDen)
	return count > bucketCnt && uintptr(count) > loadFactorNum*(bucketShift(B)/loadFactorDen)
}

// tooManyOverflowBuckets reports whether noverflow buckets is too many for a map with 1<<B buckets.
// Note that most of these overflow buckets must be in sparse use;
// if use was dense, then we'd have already triggered regular map growth.
/*
	tooManyOverflowBuckets 判断是否有太多溢出桶. 
	桶数量 >=  1<<B buckets,表示有太多溢出桶.
	注意: 大部分溢出桶的数据都是稀疏没有填满的;
	如果桶的数据是密集的，那么我们已经触发了map的扩容.
*/
func tooManyOverflowBuckets(noverflow uint16, B uint8) bool {
	// If the threshold is too low, we do extraneous work.
	// If the threshold is too high, maps that grow and shrink can hold on to lots of unused memory.
	// "too many" means (approximately) as many overflow buckets as regular buckets.
	// See incrnoverflow for more details.
    
    /*
    	如果桶数量门槛设置的太低，那就会做太多的性能开销的工作.
    	如果门槛太高, map的扩缩容会使用太多的内存.
    	"too many"的意思是（大约）溢出桶和常规桶的数量一样多.
    	参考 incrnoverflow 的详情
    */
	if B > 15 { // 当桶的数量大于2^15时，B=15
		B = 15
	}
	// The compiler doesn't see here that B < 16; mask B to generate shorter shift code.
	return noverflow >= uint16(1)<<(B&15)
}

// growing reports whether h is growing. The growth may be to the same size or bigger.
func (h *hmap) growing() bool {
	return h.oldbuckets != nil
}

// sameSizeGrow reports whether the current growth is to a map of the same size.
// sameSizeGrow 返回当前map是否是等量扩容
func (h *hmap) sameSizeGrow() bool {
	return h.flags&sameSizeGrow != 0
}

// noldbuckets calculates the number of buckets prior to the current map growth.
// noldbuckets 预先计算出map需要扩容的桶数量.
func (h *hmap) noldbuckets() uintptr {
	oldB := h.B
	if !h.sameSizeGrow() {
		oldB--
	}
	return bucketShift(oldB)
}

// oldbucketmask provides a mask that can be applied to calculate n % noldbuckets().
// oldbucketmask 适用于计算 n % noldbuckets() ，提供标记
func (h *hmap) oldbucketmask() uintptr {
	return h.noldbuckets() - 1
}

// 扩容
func growWork(t *maptype, h *hmap, bucket uintptr) {
	// make sure we evacuate the oldbucket corresponding
	// to the bucket we're about to use
    // 确认从旧桶搬运到新桶的数据是我们在使用的.
	evacuate(t, h, bucket&h.oldbucketmask())

	// evacuate one more oldbucket to make progress on growing
    // 每次搬运两次，加速扩容
	if h.growing() {
		evacuate(t, h, h.nevacuate)
	}
}

// 包装函数，直接定位到某个桶的位置搬运.
func bucketEvacuated(t *maptype, h *hmap, bucket uintptr) bool {
	b := (*bmap)(add(h.oldbuckets, bucket*uintptr(t.BucketSize)))
	return evacuated(b)
}

// evacDst is an evacuation destination.
// evacDst : 扩容信息结构体
type evacDst struct {
	b *bmap          // current destination bucket
    				 // 当前所在的桶位置
	i int            // key/elem index into b
    				 // b 桶中 key/value的位置
	k unsafe.Pointer // pointer to current key storage
    				 // 当前key的值
	e unsafe.Pointer // pointer to current elem storage
    				 // 当前value的值
}

// 扩容实现
func evacuate(t *maptype, h *hmap, oldbucket uintptr) {
    // 定位到旧桶的位置
	b := (*bmap)(add(h.oldbuckets, oldbucket*uintptr(t.BucketSize)))
	newbit := h.noldbuckets()
	if !evacuated(b) { // 未扩容完毕
		// TODO: reuse overflow buckets instead of using new ones, if there
		// is no iterator using the old buckets.  (If !oldIterator.)

		// xy contains the x and y (low and high) evacuation destinations.
        
        /*
        	TODO: (If !oldIterator.) 如果没有其他线程在迭代旧桶， 则不再创建新的桶而直接使用溢出桶.
        	
        	xy表示x和y， xy[0] = x, xy[1] = y
        */
		var xy [2]evacDst
		x := &xy[0]
		x.b = (*bmap)(add(h.buckets, oldbucket*uintptr(t.BucketSize)))
		x.k = add(unsafe.Pointer(x.b), dataOffset)
		x.e = add(x.k, bucketCnt*uintptr(t.KeySize))

		if !h.sameSizeGrow() {
			// Only calculate y pointers if we're growing bigger.
			// Otherwise GC can see bad pointers.
            
            // 双倍扩容时，只需要计算 y指针
            // 其他情况下， gc会扫描到要回收的指针.
			y := &xy[1]
			y.b = (*bmap)(add(h.buckets, (oldbucket+newbit)*uintptr(t.BucketSize)))
			y.k = add(unsafe.Pointer(y.b), dataOffset)
			y.e = add(y.k, bucketCnt*uintptr(t.KeySize))
		}

		for ; b != nil; b = b.overflow(t) { // 遍历旧桶
			k := add(unsafe.Pointer(b), dataOffset)
			e := add(k, bucketCnt*uintptr(t.KeySize))
			for i := 0; i < bucketCnt; i, k, e = i+1, add(k, uintptr(t.KeySize)), add(e, uintptr(t.ValueSize)) {
				top := b.tophash[i]
				if isEmpty(top) {
					b.tophash[i] = evacuatedEmpty // 当前元素已搬迁，下一个
					continue
				}
				if top < minTopHash { // map不正常，panic
					throw("bad map state")
				}
				k2 := k
				if t.IndirectKey() {
					k2 = *((*unsafe.Pointer)(k2))
				}
				var useY uint8
				if !h.sameSizeGrow() {
					// Compute hash to make our evacuation decision (whether we need
					// to send this key/elem to bucket x or bucket y).
                    
                    // 判断key/elem 搬迁去 x 还是 y 桶.
					hash := t.Hasher(k2, uintptr(h.hash0))
					if h.flags&iterator != 0 && !t.ReflexiveKey() && !t.Key.Equal(k2, k2) {
						// If key != key (NaNs), then the hash could be (and probably
						// will be) entirely different from the old hash. Moreover,
						// it isn't reproducible. Reproducibility is required in the
						// presence of iterators, as our evacuation decision must
						// match whatever decision the iterator made.
						// Fortunately, we have the freedom to send these keys either
						// way. Also, tophash is meaningless for these kinds of keys.
						// We let the low bit of tophash drive the evacuation decision.
						// We recompute a new random tophash for the next level so
						// these keys will get evenly distributed across all buckets
						// after multiple grows.
                        
                        /*
                        	If key != key (NaNs), 那么hash和旧hash完全不一样,
                        	而且是不可复制的. 可重复是迭代器存在的前提，所以迁移的定义必须完全匹配，而不管迭代器是如何定义的.
                        	幸运的是，有一种更自由的方式搬迁这些keys.
                        	此外,tophash 对于这种类型的key毫无意义.
                        	我们使用低位tophash去定义迁移的方向.
                        	我们会重新生成一个随机的tophash为了下一次搬迁，所以在多次扩容后，key将会倍均匀分布在所有的桶中.
                        */
						useY = top & 1
						top = tophash(hash)
					} else {
						if hash&newbit != 0 {
							useY = 1
						}
					}
				}

				if evacuatedX+1 != evacuatedY || evacuatedX^1 != evacuatedY { // 异常搬迁状态，panic.
					throw("bad evacuatedN")
				}
				
                // useY是0时，代表x，是1时，代表y，设置巧妙.
				b.tophash[i] = evacuatedX + useY // evacuatedX + 1 == evacuatedY
				dst := &xy[useY]                 // evacuation destination

				if dst.i == bucketCnt { // 最后一个cell
					dst.b = h.newoverflow(t, dst.b)
					dst.i = 0
					dst.k = add(unsafe.Pointer(dst.b), dataOffset)
					dst.e = add(dst.k, bucketCnt*uintptr(t.KeySize))
				}
				dst.b.tophash[dst.i&(bucketCnt-1)] = top // mask dst.i as an optimization, to avoid a bounds check
                										 // dst.i进行掩码优化，避免边界检查.
				if t.IndirectKey() {
					*(*unsafe.Pointer)(dst.k) = k2 // copy pointer 
				} else {
					typedmemmove(t.Key, dst.k, k) // copy elem （源码注释有误,放下面）
				}
				if t.IndirectElem() {
					*(*unsafe.Pointer)(dst.e) = *(*unsafe.Pointer)(e)
				} else {
					typedmemmove(t.Elem, dst.e, e)
				}
				dst.i++
				// These updates might push these pointers past the end of the
				// key or elem arrays.  That's ok, as we have the overflow pointer
				// at the end of the bucket to protect against pointing past the
				// end of the bucket.
                /*
                	这些更新可能会将指针超出键值对的末尾.
                	没关系，我们还有溢出指针指向桶的末尾来保护不会超过末尾.
                */
				dst.k = add(dst.k, uintptr(t.KeySize))
				dst.e = add(dst.e, uintptr(t.ValueSize))
			}
		}
		// Unlink the overflow buckets & clear key/elem to help GC.
        // 断开溢出桶的链接，帮助gc清除key/elem.
		if h.flags&oldIterator == 0 && t.Bucket.PtrBytes != 0 {
			b := add(h.oldbuckets, oldbucket*uintptr(t.BucketSize))
			// Preserve b.tophash because the evacuation
			// state is maintained there.
            // 保存 b.tophash 因为搬迁状态主要存储在这里.
			ptr := add(b, dataOffset)
			n := uintptr(t.BucketSize) - dataOffset
			memclrHasPointers(ptr, n)
		}
	}

	if oldbucket == h.nevacuate { // 旧桶数量 = 迁移进度
		advanceEvacuationMark(h, t, newbit)
	}
}

func advanceEvacuationMark(h *hmap, t *maptype, newbit uintptr) {
	h.nevacuate++
	// Experiments suggest that 1024 is overkill by at least an order of magnitude.
	// Put it in there as a safeguard anyway, to ensure O(1) behavior.
    /*
    	实验证明 迁移进度每到一个1024都会至少大一个数量级.
    	为了确保 O(1)的复杂度，必须做一些措施处理.
    	即最多标记进度为1024
    */
	stop := h.nevacuate + 1024
	if stop > newbit {
		stop = newbit
	}
	for h.nevacuate != stop && bucketEvacuated(t, h, h.nevacuate) {
		h.nevacuate++
	}
	if h.nevacuate == newbit { // newbit == # of oldbuckets
		// Growing is all done. Free old main bucket array.
        // 迁移完毕，释放旧桶内存.
		h.oldbuckets = nil
		// Can discard old overflow buckets as well.
		// If they are still referenced by an iterator,
		// then the iterator holds a pointers to the slice.
        // 可以旧的溢出桶也可以释放了. 
        // 如果仍然有迭代器指向溢出桶，那么迭代器保留指向slice.
		if h.extra != nil {
			h.extra.oldoverflow = nil
		}
		h.flags &^= sameSizeGrow
	}
}

// Reflect stubs. Called from ../reflect/asm_*.s
// 下面都是反射时用到的方法

//go:linkname reflect_makemap reflect.makemap
// 私有方法和变量导出： reflect.makemap即调用map下的reflect_makemap方法.
// 主要时对map的定义的类型的长度等校验是否符合标准.
func reflect_makemap(t *maptype, cap int) *hmap {
	// Check invariants and reflects math.
    // 检查不变的以及映射关系.
	if t.Key.Equal == nil { // map的key是否时可比较的
		throw("runtime.reflect_makemap: unsupported map key type")
	}
	if t.Key.Size_ > maxKeySize && (!t.IndirectKey() || t.KeySize != uint8(goarch.PtrSize)) ||
		t.Key.Size_ <= maxKeySize && (t.IndirectKey() || t.KeySize != uint8(t.Key.Size_)) {
		throw("key size wrong")
	}
	if t.Elem.Size_ > maxElemSize && (!t.IndirectElem() || t.ValueSize != uint8(goarch.PtrSize)) ||
		t.Elem.Size_ <= maxElemSize && (t.IndirectElem() || t.ValueSize != uint8(t.Elem.Size_)) {
		throw("elem size wrong")
	}
	if t.Key.Align_ > bucketCnt {
		throw("key align too big")
	}
	if t.Elem.Align_ > bucketCnt {
		throw("elem align too big")
	}
	if t.Key.Size_%uintptr(t.Key.Align_) != 0 {
		throw("key size not a multiple of key align")
	}
	if t.Elem.Size_%uintptr(t.Elem.Align_) != 0 {
		throw("elem size not a multiple of elem align")
	}
	if bucketCnt < 8 {
		throw("bucketsize too small for proper alignment")
	}
	if dataOffset%uintptr(t.Key.Align_) != 0 {
		throw("need padding in bucket (key)")
	}
	if dataOffset%uintptr(t.Elem.Align_) != 0 {
		throw("need padding in bucket (elem)")
	}

	return makemap(t, cap, nil)
}

//go:linkname reflect_mapaccess reflect.mapaccess
// 套 mapaccess2，主要时elem不存在时，返回nil.
func reflect_mapaccess(t *maptype, h *hmap, key unsafe.Pointer) unsafe.Pointer {
	elem, ok := mapaccess2(t, h, key)
	if !ok {
		// reflect wants nil for a missing element
		elem = nil
	}
	return elem
}

//go:linkname reflect_mapaccess_faststr reflect.mapaccess_faststr
// 套 mapaccess2_faststr
func reflect_mapaccess_faststr(t *maptype, h *hmap, key string) unsafe.Pointer {
	elem, ok := mapaccess2_faststr(t, h, key)
	if !ok {
		// reflect wants nil for a missing element
		elem = nil
	}
	return elem
}

//go:linkname reflect_mapassign reflect.mapassign0
// 套 mapassign
func reflect_mapassign(t *maptype, h *hmap, key unsafe.Pointer, elem unsafe.Pointer) {
	p := mapassign(t, h, key)
	typedmemmove(t.Elem, p, elem)
}

//go:linkname reflect_mapassign_faststr reflect.mapassign_faststr0
// 针对key是string的一个处理方法，因为大部分map的key是string
func reflect_mapassign_faststr(t *maptype, h *hmap, key string, elem unsafe.Pointer) {
	p := mapassign_faststr(t, h, key)
	typedmemmove(t.Elem, p, elem)
}

//go:linkname reflect_mapdelete reflect.mapdelete
func reflect_mapdelete(t *maptype, h *hmap, key unsafe.Pointer) {
	mapdelete(t, h, key)
}

//go:linkname reflect_mapdelete_faststr reflect.mapdelete_faststr
func reflect_mapdelete_faststr(t *maptype, h *hmap, key string) {
	mapdelete_faststr(t, h, key)
}

//go:linkname reflect_mapiterinit reflect.mapiterinit
func reflect_mapiterinit(t *maptype, h *hmap, it *hiter) {
	mapiterinit(t, h, it)
}

//go:linkname reflect_mapiternext reflect.mapiternext
func reflect_mapiternext(it *hiter) {
	mapiternext(it)
}

//go:linkname reflect_mapiterkey reflect.mapiterkey
func reflect_mapiterkey(it *hiter) unsafe.Pointer {
	return it.key
}

//go:linkname reflect_mapiterelem reflect.mapiterelem
func reflect_mapiterelem(it *hiter) unsafe.Pointer {
	return it.elem
}

//go:linkname reflect_maplen reflect.maplen
func reflect_maplen(h *hmap) int {
	if h == nil {
		return 0
	}
	if raceenabled {
		callerpc := getcallerpc()
		racereadpc(unsafe.Pointer(h), callerpc, abi.FuncPCABIInternal(reflect_maplen))
	}
	return h.count
}

//go:linkname reflect_mapclear reflect.mapclear
func reflect_mapclear(t *maptype, h *hmap) {
	mapclear(t, h)
}

//go:linkname reflectlite_maplen internal/reflectlite.maplen
func reflectlite_maplen(h *hmap) int {
	if h == nil {
		return 0
	}
	if raceenabled {
		callerpc := getcallerpc()
		racereadpc(unsafe.Pointer(h), callerpc, abi.FuncPCABIInternal(reflect_maplen))
	}
	return h.count
}

const maxZero = 1024 // must match value in reflect/value.go:maxZero cmd/compile/internal/gc/walk.go:zeroValSize
var zeroVal [maxZero]byte

// mapinitnoop is a no-op function known the Go linker; if a given global
// map (of the right size) is determined to be dead, the linker will
// rewrite the relocation (from the package init func) from the outlined
// map init function to this symbol. Defined in assembly so as to avoid
// complications with instrumentation (coverage, etc).
/*	(TODO: 不知道干嘛的)
	mapinitnoop 是一个知道 go连接者 的no-op 方法;
	如果一个全局的map（合适大小）要被释放，连接者将重定向（通过包的init方法）map到这个连接.
	避免集中并发.
	
*/
func mapinitnoop()

// mapclone for implementing maps.Clone
//
//go:linkname mapclone maps.clone

/*
	map的克隆，返回一个新的map，支持泛型any.
*/
func mapclone(m any) any {
	e := efaceOf(&m)
	e.data = unsafe.Pointer(mapclone2((*maptype)(unsafe.Pointer(e._type)), (*hmap)(e.data)))
	return m
}

// moveToBmap moves a bucket from src to dst. It returns the destination bucket or new destination bucket if it overflows
// and the pos that the next key/value will be written, if pos == bucketCnt means needs to written in overflow bucket.
// moveToBmap 是将桶从src移动到dst. 返回dst，dst可能是新分配内存的（如果是溢出桶且指针指向的key/value被重写了），如果 pos == bucketCnt意味着
// 是溢出桶的move.
func moveToBmap(t *maptype, h *hmap, dst *bmap, pos int, src *bmap) (*bmap, int) {
	for i := 0; i < bucketCnt; i++ {
		if isEmpty(src.tophash[i]) { // cell为空不用move
			continue
		}

		for ; pos < bucketCnt; pos++ {
			if isEmpty(dst.tophash[pos]) { // 跳过空cell
				break
			}
		}

		if pos == bucketCnt {
			dst = h.newoverflow(t, dst)
			pos = 0
		}

		srcK := add(unsafe.Pointer(src), dataOffset+uintptr(i)*uintptr(t.KeySize))
		srcEle := add(unsafe.Pointer(src), dataOffset+bucketCnt*uintptr(t.KeySize)+uintptr(i)*uintptr(t.ValueSize))
		dstK := add(unsafe.Pointer(dst), dataOffset+uintptr(pos)*uintptr(t.KeySize))
		dstEle := add(unsafe.Pointer(dst), dataOffset+bucketCnt*uintptr(t.KeySize)+uintptr(pos)*uintptr(t.ValueSize))

		dst.tophash[pos] = src.tophash[i]
		if t.IndirectKey() {
			*(*unsafe.Pointer)(dstK) = *(*unsafe.Pointer)(srcK)
		} else {
			typedmemmove(t.Key, dstK, srcK)
		}
		if t.IndirectElem() {
			*(*unsafe.Pointer)(dstEle) = *(*unsafe.Pointer)(srcEle)
		} else {
			typedmemmove(t.Elem, dstEle, srcEle)
		}
		pos++
		h.count++
	}
	return dst, pos
}

// clone的详细实现
func mapclone2(t *maptype, src *hmap) *hmap {
	dst := makemap(t, src.count, nil) // 创建新map空间
	dst.hash0 = src.hash0
	dst.nevacuate = 0
	//flags do not need to be copied here, just like a new map has no flags.
    // map 的 flags 无需复制，即当成新的map处理，无flags。
    
	if src.count == 0 { // 长度为0时，直接返回
		return dst
	}

	if src.flags&hashWriting != 0 { // 不支持并发写时clone（clone为非并发安全的方法）
		fatal("concurrent map clone and map write")
	}

	if src.B == 0 { // 桶的数量为0，dst新创建桶， 则直接迁移整个桶返回
		dst.buckets = newobject(t.Bucket)
		dst.count = src.count
		typedmemmove(t.Bucket, dst.buckets, src.buckets)
		return dst
	}

	//src.B != 0
	if dst.B == 0 {
		dst.buckets = newobject(t.Bucket)
	}
	dstArraySize := int(bucketShift(dst.B))
	srcArraySize := int(bucketShift(src.B))
	for i := 0; i < dstArraySize; i++ { // clone桶
		dstBmap := (*bmap)(add(dst.buckets, uintptr(i*int(t.BucketSize))))
		pos := 0
		for j := 0; j < srcArraySize; j += dstArraySize {
			srcBmap := (*bmap)(add(src.buckets, uintptr((i+j)*int(t.BucketSize))))
			for srcBmap != nil {
				dstBmap, pos = moveToBmap(t, dst, dstBmap, pos, srcBmap)
				srcBmap = srcBmap.overflow(t)
			}
		}
	}

	if src.oldbuckets == nil { // 没有扩容中的旧桶，直接退出
		return dst
	}
    
    // 有旧桶，说明正在扩容，需要将旧桶中的数据也clone
	oldB := src.B
	srcOldbuckets := src.oldbuckets
	if !src.sameSizeGrow() {
		oldB-- //非等量扩容，双倍扩容，旧桶的是新桶的一半
	}
	oldSrcArraySize := int(bucketShift(oldB))

	for i := 0; i < oldSrcArraySize; i++ {
		srcBmap := (*bmap)(add(srcOldbuckets, uintptr(i*int(t.BucketSize))))
		if evacuated(srcBmap) { // 搬运完成的旧桶，不需要再clone
			continue
		}

		if oldB >= dst.B { // main bucket bits in dst is less than oldB bits in src
            			   // 目标map的桶的数量小于src的旧桶的数量，往map的桶最后面追加clone,一直到dst的map桶大于src的旧桶数量.
			dstBmap := (*bmap)(add(dst.buckets, uintptr(i)&bucketMask(dst.B)))
			for dstBmap.overflow(t) != nil {
				dstBmap = dstBmap.overflow(t)
			}
			pos := 0
			for srcBmap != nil {
				dstBmap, pos = moveToBmap(t, dst, dstBmap, pos, srcBmap)
				srcBmap = srcBmap.overflow(t)
			}
			continue
		}

		for srcBmap != nil {
			// move from oldBlucket to new bucket
			for i := uintptr(0); i < bucketCnt; i++ {
				if isEmpty(srcBmap.tophash[i]) { // 为空的cell不用clone
					continue
				}

				if src.flags&hashWriting != 0 { // panic, 写map时，禁止clone
					fatal("concurrent map clone and map write")
				}

                // 找出key值，key调用 mapassign 直接赋值， 基于mapassign返回的，dstEle，elem的目标地址， elem直接内存操作
				srcK := add(unsafe.Pointer(srcBmap), dataOffset+i*uintptr(t.KeySize))
				if t.IndirectKey() {
					srcK = *((*unsafe.Pointer)(srcK))
				}

				srcEle := add(unsafe.Pointer(srcBmap), dataOffset+bucketCnt*uintptr(t.KeySize)+i*uintptr(t.ValueSize))
				if t.IndirectElem() {
					srcEle = *((*unsafe.Pointer)(srcEle))
				}
				dstEle := mapassign(t, dst, srcK)
				typedmemmove(t.Elem, dstEle, srcEle)
			}
			srcBmap = srcBmap.overflow(t)
		}
	}
	return dst
}

// keys for implementing maps.keys
//
//go:linkname keys maps.keys
func keys(m any, p unsafe.Pointer) {
    // 将泛型类型any（原interface）以及值转成 maptype和hmap
	e := efaceOf(&m)
	t := (*maptype)(unsafe.Pointer(e._type))
	h := (*hmap)(e.data)

	if h == nil || h.count == 0 { // map为nil或者数量为0，直接返回
		return
	}
	s := (*slice)(p) // 返回的是key的切片
	r := int(fastrand())
	offset := uint8(r >> h.B & (bucketCnt - 1)) // 随机一个地方开始进行偏移
	if h.B == 0 { // 桶数量为0，直接copy返回
		copyKeys(t, h, (*bmap)(h.buckets), s, offset)
		return
	}
	arraySize := int(bucketShift(h.B))
	buckets := h.buckets
	for i := 0; i < arraySize; i++ {
		bucket := (i + r) & (arraySize - 1)
		b := (*bmap)(add(buckets, uintptr(bucket)*uintptr(t.BucketSize)))
		copyKeys(t, h, b, s, offset)
	}

	if h.growing() { // 扩容中，旧桶的keys也要读出来
		oldArraySize := int(h.noldbuckets())
		for i := 0; i < oldArraySize; i++ {
			bucket := (i + r) & (oldArraySize - 1)
			b := (*bmap)(add(h.oldbuckets, uintptr(bucket)*uintptr(t.BucketSize)))
			if evacuated(b) {
				continue
			}
			copyKeys(t, h, b, s, offset)
		}
	}
	return
}

func copyKeys(t *maptype, h *hmap, b *bmap, s *slice, offset uint8) {
	for b != nil {
		for i := uintptr(0); i < bucketCnt; i++ {
			offi := (i + uintptr(offset)) & (bucketCnt - 1)
			if isEmpty(b.tophash[offi]) {
				continue
			}
			if h.flags&hashWriting != 0 {
				fatal("concurrent map read and map write")
			}
			k := add(unsafe.Pointer(b), dataOffset+offi*uintptr(t.KeySize))
			if t.IndirectKey() {
				k = *((*unsafe.Pointer)(k))
			}
			if s.len >= s.cap { // 实际存储的数量不应大于等量桶的创建的内存大小，否则就是在写入的时候进行了读操作
				fatal("concurrent map read and map write")
			}
			typedmemmove(t.Key, add(s.array, uintptr(s.len)*uintptr(t.KeySize)), k)
			s.len++
		}
		b = b.overflow(t)
	}
}

// values for implementing maps.values
//
// 和keys的实现完全一样，只是，偏移的时候略过key的偏移量即可.
//go:linkname values maps.values
func values(m any, p unsafe.Pointer) {
	e := efaceOf(&m)
	t := (*maptype)(unsafe.Pointer(e._type))
	h := (*hmap)(e.data)
	if h == nil || h.count == 0 {
		return
	}
	s := (*slice)(p)
	r := int(fastrand())
	offset := uint8(r >> h.B & (bucketCnt - 1))
	if h.B == 0 {
		copyValues(t, h, (*bmap)(h.buckets), s, offset)
		return
	}
	arraySize := int(bucketShift(h.B))
	buckets := h.buckets
	for i := 0; i < arraySize; i++ {
		bucket := (i + r) & (arraySize - 1)
		b := (*bmap)(add(buckets, uintptr(bucket)*uintptr(t.BucketSize)))
		copyValues(t, h, b, s, offset)
	}

	if h.growing() {
		oldArraySize := int(h.noldbuckets())
		for i := 0; i < oldArraySize; i++ {
			bucket := (i + r) & (oldArraySize - 1)
			b := (*bmap)(add(h.oldbuckets, uintptr(bucket)*uintptr(t.BucketSize)))
			if evacuated(b) {
				continue
			}
			copyValues(t, h, b, s, offset)
		}
	}
	return
}

func copyValues(t *maptype, h *hmap, b *bmap, s *slice, offset uint8) {
	for b != nil {
		for i := uintptr(0); i < bucketCnt; i++ {
			offi := (i + uintptr(offset)) & (bucketCnt - 1)
			if isEmpty(b.tophash[offi]) {
				continue
			}

			if h.flags&hashWriting != 0 {
				fatal("concurrent map read and map write")
			}

			ele := add(unsafe.Pointer(b), dataOffset+bucketCnt*uintptr(t.KeySize)+offi*uintptr(t.ValueSize))
			if t.IndirectElem() {
				ele = *((*unsafe.Pointer)(ele))
			}
			if s.len >= s.cap {
				fatal("concurrent map read and map write")
			}
			typedmemmove(t.Elem, add(s.array, uintptr(s.len)*uintptr(t.ValueSize)), ele)
			s.len++
		}
		b = b.overflow(t)
	}
}
