// Project Calico BPF dataplane programs.
// Copyright (c) 2020 Tigera, Inc. All rights reserved.
//
// This program is free software; you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation; either version 2 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License along
// with this program; if not, write to the Free Software Foundation, Inc.,
// 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.

#include "ut.h"
#include "bpf.h"
#include "skb.h"

static CALI_BPF_INLINE int calico_unittest_entry (struct __sk_buff *skb)
{
	struct iphdr *ip;

	/* emulate the size check that the caller would have done */
	if (skb_shorter(skb, ETH_IPV4_UDP_SIZE))
		return -1;

	ip = skb_iphdr(skb);
	ip_dec_ttl(ip);

	return 0;
}
