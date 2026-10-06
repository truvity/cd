// Package delivery holds the rules of the delivery charts that a platform's
// own generator needs before it renders them: how a product's version pin and
// its Kargo Project are named, which Application names the rings get, which
// availability a cluster may declare, which clusters a pipeline promotes
// through, and when an end-to-end prober may present its workload identity.
//
// The charts (cd-delivery, cd-pipeline, cd-kargo) render what they are given;
// this package computes the rows they are given, from facts the caller passes
// in. It reads no files and knows no estate.
package delivery
